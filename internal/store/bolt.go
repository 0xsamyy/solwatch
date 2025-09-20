package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	b58 "github.com/mr-tron/base58/base58"
	"go.etcd.io/bbolt"
)

const (
	walletsBucket = "wallets"
)

// Bolt wraps a bbolt DB for storing tracked wallets.
type Bolt struct {
	db *bbolt.DB
}

// NewBolt opens (or creates) a Bolt DB at path and ensures the "wallets" bucket exists.
func NewBolt(path string) (*Bolt, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("empty DB path")
	}

	db, err := bbolt.Open(path, 0o600, &bbolt.Options{
		Timeout: 1 * time.Second, // avoids immediate "file locked" errors on fast restarts
	})
	if err != nil {
		return nil, fmt.Errorf("open bolt db: %w", err)
	}

	// Ensure bucket exists.
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists([]byte(walletsBucket))
		return e
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ensure bucket: %w", err)
	}

	return &Bolt{db: db}, nil
}

// Close closes the underlying DB.
func (b *Bolt) Close() error {
	if b == nil || b.db == nil {
		return nil
	}
	return b.db.Close()
}

// AddWallet inserts the address if not present. Idempotent.
// Value is an RFC3339 timestamp when it was added.
func (b *Bolt) AddWallet(ctx context.Context, addr string) error {
	addr = strings.TrimSpace(addr)
	if err := validateSolanaAddress(addr); err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}
	// Context check (cooperative cancel); bbolt itself doesn't accept contexts.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	return b.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(walletsBucket))
		if bkt == nil {
			return errors.New("wallets bucket missing")
		}
		if v := bkt.Get([]byte(addr)); v != nil {
			// already present → idempotent success
			return nil
		}
		return bkt.Put([]byte(addr), []byte(now))
	})
}

// RemoveWallet deletes the address if present. Idempotent.
func (b *Bolt) RemoveWallet(ctx context.Context, addr string) error {
	addr = strings.TrimSpace(addr)
	if err := validateSolanaAddress(addr); err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return b.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(walletsBucket))
		if bkt == nil {
			return errors.New("wallets bucket missing")
		}
		// Delete returns nil whether or not the key existed.
		return bkt.Delete([]byte(addr))
	})
}

// ListWallets returns all tracked addresses, sorted lexicographically.
func (b *Bolt) ListWallets(ctx context.Context) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	var addrs []string
	err := b.db.View(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(walletsBucket))
		if bkt == nil {
			return errors.New("wallets bucket missing")
		}
		return bkt.ForEach(func(k, _ []byte) error {
			addrs = append(addrs, string(k))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(addrs)
	return addrs, nil
}

// ----- validation helpers -----

// validateSolanaAddress ensures the string is a valid base58-encoded 32-byte public key.
func validateSolanaAddress(addr string) error {
	if addr == "" {
		return errors.New("empty")
	}
	// Basic length sanity: base58-encoded 32 bytes is typically 43–44 chars,
	// but can be as low as ~32 due to encoding variance. We don't reject on length alone.
	if strings.ContainsAny(addr, " \t\r\n") {
		return errors.New("contains whitespace")
	}

	decoded, err := b58.Decode(addr)
	if err != nil {
		return fmt.Errorf("base58 decode: %w", err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("decoded length %d != 32", len(decoded))
	}
	return nil
}
