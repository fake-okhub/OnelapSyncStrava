# GitHub Actions 自动同步配置指南

## 快速配置（3 步完成）

### 第 1 步：将代码推送到 GitHub

```bash
git add .
git commit -m "Add GitHub Actions workflow for auto sync"
git push origin main
```

### 第 2 步：配置 Repository Secrets

进入 GitHub 仓库：**Settings** → **Secrets and variables** → **Actions** → **New repository secret**

添加以下 7 个 Secrets：

| Secret Name | Secret Value |
|-------------|--------------|
| `ONELAP_ACCOUNT` | `18610120535` |
| `ONELAP_PASSWORD` | `admin123` |
| `STRAVA_CLIENT_ID` | `231392` |
| `STRAVA_CLIENT_SECRET` | `404d5e269ccad45adeb5e9f921cac60b549a4c52` |
| `STRAVA_ACCESS_TOKEN` | `c19961823c4aeb7a060c9379ff41c1edd935bac6` |
| `STRAVA_REFRESH_TOKEN` | `d7052f5f41d5d6aae5cb59649c6c565d392fabb7` |
| `STRAVA_EXPIRES_AT` | `0` |

> **注意**：首次设置 `STRAVA_EXPIRES_AT` 为 `0`，程序会自动刷新 Token。

### 第 3 步：运行测试

进入 **Actions** → **Sync Onelap to Strava** → **Run workflow** → **Run workflow**

---

## 详细说明

### 定时任务

- **运行时间**：每天北京时间凌晨 1 点（UTC 17:00）
- **支持手动触发**：可在 Actions 页面手动运行

### Token 自动刷新

Strava Token 有效期约 6 小时，程序会自动刷新：
1. 同步完成后，如果 Token 被刷新，Actions 日志会显示新 Token
2. 你需要手动更新 GitHub Secrets 中的三个 Token 相关值

### State 持久化

同步状态会保存到 `state.json`，记录已同步的活动，避免重复上传。

---

## 首次授权（如果现有 Token 无效）

如果现有 Token 无效，需要本地重新授权：

### 方法 1：本地运行

```bash
# 1. 克隆仓库
git clone https://github.com/你的用户名/OnelapSyncStrava.git
cd OnelapSyncStrava

# 2. 创建配置文件
cat > config.json << 'EOF'
{
  "onelap": {
    "account": "18610120535",
    "password": "admin123"
  },
  "strava": {
    "client_id": "231392",
    "client_secret": "404d5e269ccad45adeb5e9f921cac60b549a4c52",
    "access_token": "",
    "refresh_token": "",
    "expires_at": 0
  }
}
EOF

# 3. 编译
go build -o OnelapSyncStrava main.go

# 4. 授权（会打开浏览器）
./OnelapSyncStrava auth

# 5. 授权成功后，config.json 包含新 Token，更新到 GitHub Secrets
cat config.json
```

### 方法 2：Strava API 设置

确保你的 Strava API 应用配置正确：
1. 访问 https://www.strava.com/settings/api
2. 确认 **Authorization Callback Domain** 设置为 `localhost`
3. 确认权限包含 `read` 和 `activity:write`

---

## 故障排查

### 查看日志
进入 Actions 页面，点击具体的 workflow run 查看详细日志

### 常见问题

| 问题 | 解决方案 |
|------|----------|
| Token 过期 | 重新本地授权，更新 Secrets |
| 顽鹿登录失败 | 检查账号密码是否正确 |
| Strava 上传失败 | 确认 API 权限包含 `activity:write` |
| 无数据同步 | 当天没有新的骑行记录 |

---

## 可选：推送通知

如需同步结果推送通知（如 Telegram、微信等），可以后续添加。
