package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	tg "github.com/go-telegram/bot"

	"github.com/0xsamyy/solwatch/internal/config"
	"github.com/0xsamyy/solwatch/internal/health"
	"github.com/0xsamyy/solwatch/internal/store"
	"github.com/0xsamyy/solwatch/internal/telegram"
	"github.com/0xsamyy/solwatch/internal/tracker"
)

func main() {
	// Human-friendly logs
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lmsgprefix)
	log.SetPrefix("solwatch ")

	// Load env/config (fatal on error with clear message)
	cfg := config.MustLoad()
	log.Println(cfg.RedactedSummary())

	// Root context that cancels on SIGINT/SIGTERM
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Open persistent store (Bolt)
	st, err := store.NewBolt(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer func() {
		if e := st.Close(); e != nil {
			log.Printf("store close: %v", e)
		}
	}()

	// Tracker manager (WS subscriptions for wallets)
	tm := tracker.NewManager(cfg.HeliusWSS, cfg.Commitment)

	// Health aggregator
	hlth := health.New(tm, st)

	// Initialize Telegram bot (long polling is default in this library)
	bot, err := tg.New(cfg.TelegramBotToken)
	if err != nil {
		log.Fatalf("telegram init: %v", err)
	}

	// Handler wires commands + activity notifications; /kill => cancel()
	th := telegram.New(bot, tm, st, hlth, cfg.TelegramAdminChatID, cancel)

	// On startup: re-subscribe to all persisted wallets
	if addrs, err := st.ListWallets(ctx); err != nil {
		log.Printf("store list: %v", err)
	} else {
		for _, a := range addrs {
			if err := tm.Track(ctx, a); err != nil {
				log.Printf("track %s: %v", a, err)
			}
		}
	}

	// Block here; returns when context is canceled (/kill or signal)
	log.Println("started; awaiting Telegram commands")
	th.Run(ctx)

	log.Println("shutdown complete")
}
