# 🔍 solwatch

A **robust Telegram bot** for monitoring activity on chosen Solana wallets.  
Built in Go for reliability, persistence, and clean integration with the Solana WebSocket API.

---

## ✨ Features

- ✅ **Track wallet activity** via Solana `accountSubscribe`
- ✅ **Real-time alerts** to Telegram (with clean HTML formatting)
- ✅ **Bulk management** of wallets (`/trackmany`, `/untrackmany`)
- ✅ **Persistence** with BoltDB (tracked wallets survive restarts)
- ✅ **Health checks** (`/health` shows dropped subscriptions)
- ✅ **Graceful reconnects** (exponential backoff + jitter)
- ✅ **Kill switch** (`/kill` shuts down the service remotely)

---

## 📸 Example (Telegram)

```

🚨 Activity Detected: ABCD...WXYZ

````

- Clickable Solscan link
- No preview banner clutter
- Clean formatting for `/tracked`, `/health`, `/help`

---

## 🚀 Quick Start

### 1. Clone & build

```bash
git clone https://github.com/0xsamyy/solwatch.git
cd solwatch
go build ./cmd/solwatch
````

### 2. Configure

Copy the example `.env`:

```bash
cp .env.example .env
```

Fill in your values:

```dotenv
TELEGRAM_BOT_TOKEN=123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11
TELEGRAM_ADMIN_CHAT_ID=123456789
HELIUS_WSS=wss://mainnet.helius-rpc.com/?api-key=YOUR_KEY
DB_PATH=solwatch.db
COMMITMENT=processed
```

### 3. Run

```bash
go run ./cmd/solwatch
```

---

## 🛠 Commands

| Command                            | Description                                 |
| ---------------------------------- | ------------------------------------------- |
| `/help`                            | Show available commands                     |
| `/track <address>`                 | Start tracking a wallet                     |
| `/untrack <address>`               | Stop tracking a wallet                      |
| `/trackmany <addr1> <addr2> ...`   | Track multiple wallets at once              |
| `/untrackmany <addr1> <addr2> ...` | Remove multiple wallets                     |
| `/tracked`                         | Show all currently tracked wallets          |
| `/health`                          | Show service stats (tracked, open, dropped) |
| `/kill`                            | Kill switch — cleanly shuts down the bot    |

---

## ⚙️ Tech Details

* **Language**: Go 1.25
* **Database**: BoltDB (lightweight embedded store)
* **Networking**: Gorilla WebSocket
* **Telegram API**: `github.com/go-telegram/bot`
* **Backoff**: custom exponential backoff with jitter
* **Deployment**: runs anywhere Go runs (Linux, Docker, etc.)

---

## 🔒 Robustness

* Automatic reconnects on network errors
* Heartbeat pings to keep connections alive
* Exponential backoff with jitter for retries
* Persistent storage of tracked wallets
* Graceful shutdown on `/kill` or SIGTERM

---

## 🧑‍💻 Author

[![GitHub](https://img.shields.io/badge/GitHub-0xsamyy-black?logo=github)](https://github.com/0xsamyy)
[![Telegram](https://img.shields.io/badge/Telegram-@ox__fbac-2CA5E0?logo=telegram&logoColor=white)](https://t.me/ox_fbac)

Vibecoded all the way so don't give me too much credit. Thx GPT <3