package telegram

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/0xsamyy/solwatch/internal/health"
	"github.com/0xsamyy/solwatch/internal/tracker"
)

// WalletStore is the minimal interface we need from the persistence layer.
type WalletStore interface {
	AddWallet(ctx context.Context, addr string) error
	RemoveWallet(ctx context.Context, addr string) error
	ListWallets(ctx context.Context) ([]string, error)
}

// Handler coordinates Telegram <-> tracker/store/health.
type Handler struct {
	bot     *tg.Bot
	adminID int64
	tm      *tracker.Manager
	st      WalletStore
	hlth    *health.Health

	// killFn should gracefully shut down the service (cancel context or exit).
	killFn func()
}

// New constructs the Telegram Handler and wires Activity notifications.
// - bot: an initialized *tg.Bot
// - tm: tracker manager
// - st: wallet store
// - hlth: health aggregator
// - adminID: numeric chat id allowed to control the bot
// - killFn: function invoked on /kill (pass a context cancel from main)
func New(bot *tg.Bot, tm *tracker.Manager, st WalletStore, hlth *health.Health, adminID int64, killFn func()) *Handler {
	h := &Handler{
		bot:     bot,
		adminID: adminID,
		tm:      tm,
		st:      st,
		hlth:    hlth,
		killFn:  killFn,
	}

	// Bridge tracker -> Telegram (one-line HTML message).
	tracker.ActivityNotify = func(text string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Always send to the single admin chat.
		h.sendHTML(ctx, adminID, text)
	}

	return h
}

// Run starts long-polling and handles updates until ctx is done.
func (h *Handler) Run(ctx context.Context) {
	// Register a single default handler that processes messages.
	h.bot.RegisterHandler(tg.HandlerTypeMessageText, "", tg.MatchTypePrefix, func(c context.Context, b *tg.Bot, u *models.Update) {
		// Only accept messages from the configured admin chat.
		if u.Message == nil || u.Message.Chat == nil || u.Message.Chat.ID != h.adminID {
			return
		}
		h.handleCommand(c, u.Message)
	})

	// Start long-polling. This blocks until ctx is canceled.
	h.bot.Start(ctx)
}

func (h *Handler) handleCommand(ctx context.Context, m *models.Message) {
	raw := strings.TrimSpace(m.Text)
	lower := strings.ToLower(raw)

	switch {
	case strings.HasPrefix(lower, "/help"):
		h.replyHelp(ctx, m.Chat.ID)

	case strings.HasPrefix(lower, "/track"):
		arg := strings.TrimSpace(strings.TrimPrefix(raw, "/track"))
		if arg == "" {
			h.sendHTML(ctx, m.Chat.ID, "usage: <code>/track &lt;address&gt;</code>")
			return
		}
		// Persist first, then start tracking.
		if err := h.st.AddWallet(ctx, arg); err != nil {
			h.sendHTML(ctx, m.Chat.ID, fmt.Sprintf("track failed: <code>%v</code>", err))
			return
		}
		if err := h.tm.Track(ctx, arg); err != nil {
			h.sendHTML(ctx, m.Chat.ID, fmt.Sprintf("subscriber failed: <code>%v</code>", err))
			return
		}
		h.sendHTML(ctx, m.Chat.ID, "tracking <b>"+escapeHTML(arg)+"</b>")

	case strings.HasPrefix(lower, "/untrack"):
		arg := strings.TrimSpace(strings.TrimPrefix(raw, "/untrack"))
		if arg == "" {
			h.sendHTML(ctx, m.Chat.ID, "usage: <code>/untrack &lt;address&gt;</code>")
			return
		}
		_ = h.tm.Untrack(ctx, arg) // best-effort
		if err := h.st.RemoveWallet(ctx, arg); err != nil {
			h.sendHTML(ctx, m.Chat.ID, fmt.Sprintf("untrack failed: <code>%v</code>", err))
			return
		}
		h.sendHTML(ctx, m.Chat.ID, "untracked <b>"+escapeHTML(arg)+"</b>")

	case strings.HasPrefix(lower, "/tracked"):
		list := h.tm.List()
		if len(list) == 0 {
			h.sendHTML(ctx, m.Chat.ID, "none")
			return
		}
		var b strings.Builder
		for _, a := range list {
			b.WriteString(escapeHTML(a))
			b.WriteByte('\n')
		}
		h.sendHTML(ctx, m.Chat.ID, b.String())

	case strings.HasPrefix(lower, "/health"):
		rep := h.hlth.Snapshot(ctx)
		msg := fmt.Sprintf(
			"tracked (mem): <b>%d</b>\nopen subs: <b>%d</b>\ndropped: <b>%d</b>\ntracked (store): <b>%d</b>\nwhen: <code>%s</code>",
			rep.Tracked, rep.Open, len(rep.Dropped), rep.TrackedPersisted, rep.GeneratedAt.Format(time.RFC3339),
		)
		h.sendHTML(ctx, m.Chat.ID, msg)

	case strings.HasPrefix(lower, "/kill"):
		h.sendHTML(ctx, m.Chat.ID, "shutting down…")
		// Give Telegram a moment to flush before stopping.
		go func() {
			time.Sleep(200 * time.Millisecond)
			if h.killFn != nil {
				h.killFn()
			} else {
				log.Println("[telegram] killFn not set")
			}
		}()

	default:
		h.sendHTML(ctx, m.Chat.ID, "unknown command. try <code>/help</code>")
	}
}

func (h *Handler) replyHelp(ctx context.Context, chatID int64) {
	help := strings.TrimSpace(`
<b>solwatch</b>
/help - show this help
/track &lt;address&gt; - start tracking a wallet
/untrack &lt;address&gt; - stop tracking a wallet
/tracked - list tracked wallets
/health - show counts and dropped subscriptions
/kill - shutdown the service
`)
	h.sendHTML(ctx, chatID, help)
}

// sendHTML sends a Telegram message using HTML parse mode.
func (h *Handler) sendHTML(ctx context.Context, chatID int64, html string) {
	_, err := h.bot.SendMessage(ctx, &tg.SendMessageParams{
		ChatID:    chatID,
		Text:      html,
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		log.Printf("[telegram] send error: %v", err)
	}
}

// escapeHTML escapes minimal characters for safe HTML messages.
// We rely on Telegram's HTML parse mode; only a tiny subset of tags used (<b>, <code>, <a>).
func escapeHTML(s string) string {
	replacer := strings.NewReplacer(
		`&`, "&amp;",
		`<`, "&lt;",
		`>`, "&gt;",
		`"`, "&quot;",
	)
	return replacer.Replace(s)
}
