package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"OnelapSyncStrava/internal/config"
	"OnelapSyncStrava/internal/onelap"
	"OnelapSyncStrava/internal/strava"
)

const (
	configPath = "config.json"
	statePath  = "state.json"
)

func main() {
	if err := config.LoadConfig(configPath); err != nil {
		log.Fatalf("Error loading config: %v", err)
	}
	if err := config.LoadState(statePath); err != nil {
		log.Fatalf("Error loading state: %v", err)
	}

	command := "sync"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "auth":
		if err := strava.Authorize(configPath); err != nil {
			log.Fatalf("Strava authorization error: %v", err)
		}
	case "check":
		runCheck()
	case "status":
		runStatus()
	case "sync":
		runSync()
	case "sync-all":
		runSyncAll()
	case "download-all":
		runDownloadAll()
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println("OnelapSyncStrava - Sync Onelap activities to Strava")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  OnelapSyncStrava [command]")
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  sync          (default) Fetch today's activities and upload to Strava")
	fmt.Println("  sync-all      Sync ALL historical activities to Strava (with rate limiting)")
	fmt.Println("  download-all  Download ALL FIT files from Onelap (with rate limiting)")
	fmt.Println("  auth          Run Strava OAuth flow to get access tokens")
	fmt.Println("  check         Verify credentials and connectivity")
	fmt.Println("  status        Show current configuration and sync status")
}

func runCheck() {
	onelapClient := onelap.NewClient()
	fmt.Print("Checking Onelap connectivity... ")
	if err := onelapClient.Check(config.GlobalConfig.Onelap.Account, config.GlobalConfig.Onelap.Password); err != nil {
		fmt.Printf("FAILED: %v\n", err)
	} else {
		fmt.Println("SUCCESS")
	}

	stravaClient := strava.NewClient()
	fmt.Print("Checking Strava connectivity...  ")
	if err := stravaClient.Check(configPath); err != nil {
		fmt.Printf("FAILED: %v\n", err)
	} else {
		fmt.Println("SUCCESS")
	}
}

func runStatus() {
	fmt.Println("--- Configuration Status ---")
	fmt.Printf("Onelap Account:  %s\n", config.GlobalConfig.Onelap.Account)
	fmt.Printf("Strava ClientID: %s\n", config.GlobalConfig.Strava.ClientID)

	hasToken := "No"
	if config.GlobalConfig.Strava.RefreshToken != "" {
		hasToken = "Yes"
	}
	fmt.Printf("Strava Authed:   %s\n", hasToken)

	fmt.Printf("\n--- Sync Status ---\n")
	fmt.Printf("Synced Activities: %d\n", len(config.GlobalState.SyncedIDs))
}

func runSync() {
	onelapClient := onelap.NewClient()
	stravaClient := strava.NewClient()

	// 1. Login to Onelap
	log.Println("Logging in to Onelap...")
	if err := onelapClient.Login(config.GlobalConfig.Onelap.Account, config.GlobalConfig.Onelap.Password); err != nil {
		log.Fatalf("Onelap login error: %v", err)
	}

	// 2. Get today's activities
	log.Println("Fetching today's activities from Onelap...")
	activities, err := onelapClient.GetTodayActivities()
	if err != nil {
		log.Fatalf("Error getting today's activities: %v", err)
	}

	if len(activities) == 0 {
		log.Println("No activities found for today.")
		return
	}

	log.Printf("Found %d activities to check.", len(activities))

	// 3. Refresh Strava token
	log.Println("Refreshing Strava token...")
	if err := stravaClient.RefreshToken(configPath); err != nil {
		log.Fatalf("Strava token refresh error: %v", err)
	}

	// 4. Download and Upload
	tmpDir := "tmp"
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		log.Fatalf("Error creating tmp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	syncedCount := 0
	for _, act := range activities {
		idStr := act.ExternalID

		if config.IsSynced(idStr) {
			log.Printf("Activity %s already synced, skipping.", idStr)
			continue
		}

		log.Printf("Processing activity: %s (%s)", idStr, act.StartTime)

		fitPath := filepath.Join(tmpDir, fmt.Sprintf("%s.fit", idStr))
		log.Printf("Downloading FIT file...")
		if err := onelapClient.DownloadActivityFIT(&act, fitPath); err != nil {
			log.Printf("Error downloading FIT for activity %s: %v", idStr, err)
			continue
		}

		log.Printf("Uploading to Strava...")
		// Generate activity name: "OneLap Bike - 2026-04-26 15:39"
		activityName := fmt.Sprintf("OneLap Bike - %s", act.StartTime)
		if err := stravaClient.UploadActivity(fitPath, idStr, activityName); err != nil {
			log.Printf("Error uploading to Strava: %v", err)
		} else {
			log.Printf("Successfully synced activity %s", idStr)
			config.AddSyncedID(idStr)
			syncedCount++
		}
	}

	if syncedCount > 0 {
		if err := config.SaveState(statePath); err != nil {
			log.Printf("Warning: failed to save state: %v", err)
		}
	}
	log.Printf("Sync complete. %d new activities synced.", syncedCount)
}

// runSyncAll syncs ALL historical activities to Strava with proper rate limiting
// Strava API limits: 200 requests per 15 minutes, 2000 per day
func runSyncAll() {
	// Rate limiting configuration for Strava API
	const (
		maxRequestsPerWindow = 180                    // Leave buffer below 200 limit
		windowDuration       = 15 * time.Minute       // 15 minute window
		delayBetweenUploads  = 500 * time.Millisecond // Small delay between uploads
	)

	onelapClient := onelap.NewClient()
	stravaClient := strava.NewClient()

	// 1. Login to Onelap
	log.Println("Logging in to Onelap...")
	if err := onelapClient.Login(config.GlobalConfig.Onelap.Account, config.GlobalConfig.Onelap.Password); err != nil {
		log.Fatalf("Onelap login error: %v", err)
	}

	// 2. Get ALL activities
	log.Println("Fetching ALL activities from Onelap...")
	activities, err := onelapClient.GetActivities()
	if err != nil {
		log.Fatalf("Error getting activities: %v", err)
	}

	log.Printf("Found %d total activities.", len(activities))

	// 3. Filter out already synced activities
	var toSync []onelap.Activity
	for _, act := range activities {
		if !config.IsSynced(act.ExternalID) {
			toSync = append(toSync, act)
		}
	}

	if len(toSync) == 0 {
		log.Println("All activities already synced!")
		return
	}

	log.Printf("%d activities need to be synced.", len(toSync))

	// 4. Calculate batches needed
	batches := (len(toSync) + maxRequestsPerWindow - 1) / maxRequestsPerWindow
	log.Printf("Will process in %d batch(es) due to Strava rate limits.", batches)

	// 5. Create temp directory for FIT files
	tmpDir := "tmp"
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		log.Fatalf("Error creating tmp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 6. Process activities in batches
	syncedCount := 0
	failedCount := 0
	requestsInWindow := 0

	for i, act := range toSync {
		idStr := act.ExternalID

		// Check if we need to wait for rate limit
		if requestsInWindow >= maxRequestsPerWindow {
			log.Printf("Rate limit reached (%d requests). Waiting 15 minutes...", requestsInWindow)
			log.Printf("Progress: %d/%d synced", syncedCount, len(toSync))
			time.Sleep(windowDuration)
			requestsInWindow = 0

			// Refresh token after waiting
			log.Println("Refreshing Strava token...")
			if err := stravaClient.RefreshToken(configPath); err != nil {
				log.Fatalf("Strava token refresh error: %v", err)
			}
		}

		// Refresh token on first upload
		if syncedCount == 0 && requestsInWindow == 0 {
			log.Println("Refreshing Strava token...")
			if err := stravaClient.RefreshToken(configPath); err != nil {
				log.Fatalf("Strava token refresh error: %v", err)
			}
			requestsInWindow++ // Token refresh counts as a request
		}

		log.Printf("[%d/%d] Processing activity: %s (%s)", i+1, len(toSync), idStr, act.StartTime)

		// Download FIT file
		fitPath := filepath.Join(tmpDir, fmt.Sprintf("%s.fit", idStr))
		if err := onelapClient.DownloadActivityFIT(&act, fitPath); err != nil {
			log.Printf("Error downloading FIT for activity %s: %v", idStr, err)
			failedCount++
			continue
		}

		// Upload to Strava
		activityName := fmt.Sprintf("OneLap Bike - %s", act.StartTime)
		if err := stravaClient.UploadActivity(fitPath, idStr, activityName); err != nil {
			log.Printf("Error uploading to Strava: %v", err)
			failedCount++
		} else {
			log.Printf("Successfully synced activity %s", idStr)
			config.AddSyncedID(idStr)
			syncedCount++
			requestsInWindow++

			// Save state every 10 activities
			if syncedCount%10 == 0 {
				if err := config.SaveState(statePath); err != nil {
					log.Printf("Warning: failed to save state: %v", err)
				}
			}
		}

		// Small delay between uploads
		time.Sleep(delayBetweenUploads)
	}

	// Final state save
	if syncedCount > 0 {
		if err := config.SaveState(statePath); err != nil {
			log.Printf("Warning: failed to save state: %v", err)
		}
	}

	log.Printf("Sync-all complete!")
	log.Printf("  Synced: %d", syncedCount)
	log.Printf("  Failed: %d", failedCount)
	log.Printf("  Total activities now in state: %d", len(config.GlobalState.SyncedIDs))
}

func runDownloadAll() {
	// Safety: rate limiting configuration
	const (
		delayBetweenDownloads = 2 * time.Second  // Delay between each download
		delayBetweenBatches   = 10 * time.Second // Delay every 10 files
		batchSize             = 10
		maxDownloads          = 0 // Set to 0 for unlimited downloads
	)

	onelapClient := onelap.NewClient()

	// 1. Login to Onelap
	log.Println("Logging in to Onelap...")
	if err := onelapClient.Login(config.GlobalConfig.Onelap.Account, config.GlobalConfig.Onelap.Password); err != nil {
		log.Fatalf("Onelap login error: %v", err)
	}

	// 2. Get ALL activities
	log.Println("Fetching ALL activities from Onelap...")
	activities, err := onelapClient.GetActivities()
	if err != nil {
		log.Fatalf("Error getting activities: %v", err)
	}

	log.Printf("Found %d total activities.", len(activities))

	// Test mode: limit downloads
	if maxDownloads > 0 && len(activities) > maxDownloads {
		log.Printf("TEST MODE: Only processing first %d activities.", maxDownloads)
		activities = activities[:maxDownloads]
	}

	// 3. Create output directory
	outputDir := "fit_downloads"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Error creating output dir: %v", err)
	}

	// 4. Download all FIT files
	downloadedCount := 0
	skippedCount := 0
	failedCount := 0

	for i, act := range activities {
		idStr := act.ExternalID

		// Check if already downloaded
		fitPath := filepath.Join(outputDir, fmt.Sprintf("%s.fit", idStr))
		if _, err := os.Stat(fitPath); err == nil {
			log.Printf("[%d/%d] Activity %s already downloaded, skipping.", i+1, len(activities), idStr)
			skippedCount++
			continue
		}

		log.Printf("[%d/%d] Downloading activity: %s (%s)", i+1, len(activities), idStr, act.StartTime)

		if err := onelapClient.DownloadActivityFIT(&act, fitPath); err != nil {
			log.Printf("Error downloading FIT for activity %s: %v", idStr, err)
			failedCount++
			continue
		}

		downloadedCount++
		log.Printf("Successfully downloaded: %s", fitPath)

		// Rate limiting: delay between downloads
		time.Sleep(delayBetweenDownloads)

		// Extra delay every batch
		if (i+1)%batchSize == 0 {
			log.Printf("Batch complete, pausing for %v to avoid rate limiting...", delayBetweenBatches)
			time.Sleep(delayBetweenBatches)
		}
	}

	log.Printf("Download complete.")
	log.Printf("  Downloaded: %d", downloadedCount)
	log.Printf("  Skipped (already exists): %d", skippedCount)
	log.Printf("  Failed: %d", failedCount)
	log.Printf("  Output directory: %s", outputDir)
}
