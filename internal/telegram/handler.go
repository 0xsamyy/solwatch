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
		if u.Message == nil {
			return
		}
		if u.Message.Chat.ID != h.adminID {
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

	// Strip bot username suffix (e.g. "/health@mybot" -> "/health")
	if idx := strings.IndexRune(lower, '@'); idx != -1 {
		lower = lower[:idx]
		raw = raw[:idx]
	}

	switch {
	case lower == "/help":
		h.replyHelp(ctx, m.Chat.ID)

	case strings.HasPrefix(lower, "/track "):
		arg := strings.TrimSpace(raw[len("/track"):])
		if arg == "" {
			h.sendHTML(ctx, m.Chat.ID, "usage: <code>/track &lt;address&gt;</code>")
			return
		}
		if err := h.st.AddWallet(ctx, arg); err != nil {
			h.sendHTML(ctx, m.Chat.ID, fmt.Sprintf("track failed: <code>%v</code>", err))
			return
		}
		if err := h.tm.Track(ctx, arg); err != nil {
			h.sendHTML(ctx, m.Chat.ID, fmt.Sprintf("subscriber failed: <code>%v</code>", err))
			return
		}
		h.sendHTML(ctx, m.Chat.ID, "tracking <b>"+escapeHTML(arg)+"</b>")

	case strings.HasPrefix(lower, "/untrack "):
		arg := strings.TrimSpace(raw[len("/untrack"):])
		if arg == "" {
			h.sendHTML(ctx, m.Chat.ID, "usage: <code>/untrack &lt;address&gt;</code>")
			return
		}
		_ = h.tm.Untrack(ctx, arg)
		if err := h.st.RemoveWallet(ctx, arg); err != nil {
			h.sendHTML(ctx, m.Chat.ID, fmt.Sprintf("untrack failed: <code>%v</code>", err))
			return
		}
		h.sendHTML(ctx, m.Chat.ID, "untracked <b>"+escapeHTML(arg)+"</b>")

	case strings.HasPrefix(lower, "/trackmany "):
		args := strings.Fields(raw[len("/trackmany"):])
		if len(args) == 0 {
			h.sendHTML(ctx, m.Chat.ID, "usage: <code>/trackmany &lt;addr1&gt; &lt;addr2&gt; ...</code>")
			return
		}
		var added, failed int
		for _, addr := range args {
			if err := h.st.AddWallet(ctx, addr); err != nil {
				failed++
				continue
			}
			if err := h.tm.Track(ctx, addr); err != nil {
				// rollback from store so DB doesn’t get out of sync
				_ = h.st.RemoveWallet(ctx, addr)
				failed++
				continue
			}
			added++
		}
		summary := fmt.Sprintf("trackmany done: added=%d failed=%d", added, failed)
		h.sendHTML(ctx, m.Chat.ID, summary)

	case strings.HasPrefix(lower, "/untrackmany "):
		args := strings.Fields(raw[len("/untrackmany"):])
		if len(args) == 0 {
			h.sendHTML(ctx, m.Chat.ID, "usage: <code>/untrackmany &lt;addr1&gt; &lt;addr2&gt; ...</code>")
			return
		}
		var removed, failed int
		for _, addr := range args {
			_ = h.tm.Untrack(ctx, addr)
			if err := h.st.RemoveWallet(ctx, addr); err != nil {
				failed++
				continue
			}
			removed++
		}
		summary := fmt.Sprintf("untrackmany done: removed=%d failed=%d", removed, failed)
		h.sendHTML(ctx, m.Chat.ID, summary)

	case lower == "/tracked":
		list := h.tm.List()
		if len(list) == 0 {
			h.sendHTML(ctx, m.Chat.ID, "<b>No wallets tracked.</b>")
			return
		}
		var b strings.Builder
		b.WriteString("<b>📋 Tracked Wallets:</b>\n")
		for _, a := range list {
			b.WriteString("• <code>")
			b.WriteString(escapeHTML(a))
			b.WriteString("</code>\n")
		}
		h.sendHTML(ctx, m.Chat.ID, b.String())

	case lower == "/health":
		rep := h.hlth.Snapshot(ctx)
		msg := fmt.Sprintf(
			"<b>📊 Health Report</b>\n"+
				"• Tracked (memory): <code>%d</code>\n"+
				"• Open subs: <code>%d</code>\n"+
				"• Dropped: <code>%d</code>\n"+
				"• Tracked (store): <code>%d</code>\n"+
				"• Time: <code>%s</code>",
			rep.Tracked, rep.Open, len(rep.Dropped), rep.TrackedPersisted, rep.GeneratedAt.Format(time.RFC3339),
		)
		h.sendHTML(ctx, m.Chat.ID, msg)

	case lower == "/kill":
		h.sendHTML(ctx, m.Chat.ID, "shutting down…")
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
<b>🛠 solwatch bot</b>

<b>Commands:</b>
• <code>/help</code> – show this help
• <code>/track &lt;address&gt;</code> – start tracking a wallet
• <code>/untrack &lt;address&gt;</code> – stop tracking a wallet
• <code>/trackmany &lt;addr1&gt; &lt;addr2&gt; ...</code> – add multiple wallets
• <code>/untrackmany &lt;addr1&gt; &lt;addr2&gt; ...</code> – remove multiple wallets
• <code>/tracked</code> – list tracked wallets
• <code>/health</code> – show counts and dropped subscriptions
• <code>/kill</code> – shutdown the service
`)
	h.sendHTML(ctx, chatID, help)
}

// sendHTML sends a Telegram message using HTML parse mode.
func (h *Handler) sendHTML(ctx context.Context, chatID int64, html string) {
	disable := true
	_, err := h.bot.SendMessage(ctx, &tg.SendMessageParams{
		ChatID:    chatID,
		Text:      html,
		ParseMode: models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{
			IsDisabled: &disable,
		},
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
