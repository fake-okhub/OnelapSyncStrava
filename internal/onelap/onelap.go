package onelap

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"
)

const (
	OnelapSecret    = "fe9f8382418fcdeb136461cac6acae7b"
	LoginBaseURL    = "https://www.onelap.cn/api"
	AnalysisBaseURL = "https://u.onelap.cn/api/otm/ride_record"
)

type Client struct {
	restyClient *resty.Client
	UID         string
	AuthToken   string
}

func NewClient() *Client {
	client := resty.New().
		SetTimeout(30 * time.Second).
		SetRetryCount(3).
		SetRetryWaitTime(2 * time.Second).
		SetRetryMaxWaitTime(10 * time.Second)

	return &Client{
		restyClient: client,
	}
}

func md5Hex(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func randomHex(n int) string {
	const hexChars = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hexChars[rand.Intn(len(hexChars))]
	}
	return string(b)
}

func (c *Client) Login(account, password string) error {
	if account == "" || password == "" {
		return fmt.Errorf("onelap account and password cannot be empty")
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := randomHex(16)
	passwordMd5 := md5Hex(password)

	signStr := fmt.Sprintf("account=%s&nonce=%s&password=%s&timestamp=%s&key=%s", account, nonce, passwordMd5, timestamp, OnelapSecret)
	sign := md5Hex(signStr)

	body := fmt.Sprintf(`{"account":"%s","password":"%s"}`, account, passwordMd5)

	resp, err := c.restyClient.R().
		SetHeader("Content-Type", "application/json").
		SetHeader("nonce", nonce).
		SetHeader("timestamp", timestamp).
		SetHeader("sign", sign).
		SetBody(body).
		Post(LoginBaseURL + "/login")

	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}

	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("login failed with status: %s, body: %s", resp.Status(), resp.String())
	}

	type LoginResponse struct {
		Data []struct {
			Token        string `json:"token"`
			RefreshToken string `json:"refresh_token"`
			UserInfo     struct {
				UID json.Number `json:"uid"`
			} `json:"userinfo"`
		} `json:"data"`
	}

	var loginData LoginResponse
	if err := json.Unmarshal(resp.Body(), &loginData); err != nil {
		return fmt.Errorf("failed to unmarshal login response: %w", err)
	}

	if len(loginData.Data) == 0 {
		return fmt.Errorf("invalid login response: no data")
	}

	c.UID = loginData.Data[0].UserInfo.UID.String()
	c.AuthToken = loginData.Data[0].Token

	return nil
}

func (c *Client) Check(account, password string) error {
	return c.Login(account, password)
}

type Activity struct {
	ExternalID string `json:"id"`              // Activity ID (new API uses "id" instead of "_id")
	Name       string `json:"name"`            // Activity name
	FitURL     string `json:"fitUrl,omitempty"` // FIT file name (from detail API, not in list)
	StartTime  string `json:"start_riding_time"` // Start time (new API uses "start_riding_time")
}

// GetActivities fetches all activities from Onelap
func (c *Client) GetActivities() ([]Activity, error) {
	resp, err := c.restyClient.R().
		SetHeader("Content-Type", "application/json").
		SetHeader("Authorization", "Bearer "+c.AuthToken).
		SetBody(`{}`).
		Post(AnalysisBaseURL + "/list")

	if err != nil {
		return nil, fmt.Errorf("get activity list failed: %w", err)
	}

	type ListResponse struct {
		Code int    `json:"code"`
		Data struct {
			List []Activity `json:"list"`
		} `json:"data"`
	}

	var dataResponse ListResponse
	if err := json.Unmarshal(resp.Body(), &dataResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal activity list: %w", err)
	}

	if dataResponse.Code != 200 {
		return nil, fmt.Errorf("activity list API returned code %d", dataResponse.Code)
	}

	return dataResponse.Data.List, nil
}

func (c *Client) GetTodayActivities() ([]Activity, error) {
	all, err := c.GetActivities()
	if err != nil {
		return nil, err
	}

	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().Add(-24 * time.Hour).Format("2006-01-02")

	var todayActivities []Activity
	for _, act := range all {
		if len(act.StartTime) >= 10 {
			dateStr := act.StartTime[:10]
			if dateStr == today || dateStr == yesterday {
				todayActivities = append(todayActivities, act)
			}
		}
	}

	return todayActivities, nil
}

// getActivityDetail fetches the detail of an activity (including fitUrl)
func (c *Client) getActivityDetail(activityID string) (string, error) {
	resp, err := c.restyClient.R().
		SetHeader("Authorization", "Bearer "+c.AuthToken).
		Get(AnalysisBaseURL + "/analysis/" + activityID)

	if err != nil {
		return "", fmt.Errorf("get activity detail failed: %w", err)
	}

	type DetailResponse struct {
		Code int `json:"code"`
		Data struct {
			RidingRecord struct {
				FitURL string `json:"fitUrl"`
			} `json:"ridingRecord"`
		} `json:"data"`
	}

	var detailResponse DetailResponse
	if err := json.Unmarshal(resp.Body(), &detailResponse); err != nil {
		return "", fmt.Errorf("failed to unmarshal activity detail: %w", err)
	}

	if detailResponse.Code != 200 {
		return "", fmt.Errorf("activity detail API returned code %d", detailResponse.Code)
	}

	return detailResponse.Data.RidingRecord.FitURL, nil
}

// DownloadActivityFIT downloads the FIT file for an activity
func (c *Client) DownloadActivityFIT(act *Activity, destPath string) error {
	// If fitUrl is not in the list response, fetch it from the detail API
	fitURL := act.FitURL
	if fitURL == "" {
		fetchedFitURL, err := c.getActivityDetail(act.ExternalID)
		if err != nil {
			return fmt.Errorf("failed to get activity detail for %s: %w", act.ExternalID, err)
		}
		fitURL = fetchedFitURL
	}

	if fitURL == "" {
		return fmt.Errorf("no fitUrl available for activity %s", act.ExternalID)
	}

	// Base64 encode the fitUrl (matching Onelap's JS: unescape(encodeURIComponent(t)) then btoa)
	encoded := base64.StdEncoding.EncodeToString([]byte(fitURL))

	downloadURL := AnalysisBaseURL + "/analysis/fit_content/" + encoded

	resp, err := c.restyClient.R().
		SetOutput(destPath).
		SetHeader("Authorization", "Bearer "+c.AuthToken).
		Get(downloadURL)

	if err != nil {
		return fmt.Errorf("failed to download FIT file: %w", err)
	}

	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("download failed with status: %s, url: %s", resp.Status(), downloadURL)
	}

	return nil
}
