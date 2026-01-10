package libwallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dexmnemonic "decred.org/dcrdex/client/mnemonic"
	"github.com/decred/libwallet/dcr"
	"github.com/decred/libwallet/mnemonic"
)

// -----------------------------------------------------------------------------
// Wallet Management
// -----------------------------------------------------------------------------

// CreateWallet creates a new wallet with the given config JSON.
func CreateWallet(configJSON string) (string, error) {
	walletsMtx.Lock()
	defer walletsMtx.Unlock()
	if !initialized {
		return "", errors.New("libwallet is not initialized")
	}

	var cfg Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return "", fmt.Errorf("malformed config: %v", err)
	}

	name := cfg.Name
	if _, exists := wallets[name]; exists {
		return "", fmt.Errorf("wallet already exists with name: %q", name)
	}

	logger := logBackend.SubLogger(name)
	params := dcr.CreateWalletParams{
		OpenWalletParams: dcr.OpenWalletParams{
			Net:      cfg.Net,
			DataDir:  cfg.DataDir,
			DbDriver: "bdb", // keep consistent with current mobile setup
			Logger:   logger,
		},
		Pass: []byte(cfg.Pass),
	}

	var recoveryConfig *dcr.RecoveryCfg
	if cfg.Mnemonic != "" {
		var (
			seed     []byte
			birthday time.Time
			seedType dcr.SeedType
			err      error
		)
		nWords := len(strings.Fields(cfg.Mnemonic))
		switch nWords {
		case 15:
			seed, birthday, err = dexmnemonic.DecodeMnemonic(cfg.Mnemonic)
			seedType = dcr.STFifteenWords
		case 12:
			seed, err = mnemonic.DecodeMnemonic(cfg.Mnemonic)
			birthday = time.Unix(cfg.Birthday, 0)
			seedType = dcr.STTwelveWords
		case 24:
			seed, err = mnemonic.DecodeMnemonic(cfg.Mnemonic)
			birthday = time.Unix(cfg.Birthday, 0)
			seedType = dcr.STTwentyFourWords
		default:
			return "", fmt.Errorf("unknown mnemonic format. expected 12, 15, or 24 words, got %d", nWords)
		}
		if err != nil {
			return "", fmt.Errorf("unable to decode wallet mnemonic: %v", err)
		}
		recoveryConfig = &dcr.RecoveryCfg{
			Seed:     seed,
			SeedPass: []byte(cfg.SeedPass),
			SeedType: seedType,
			Birthday: birthday,
		}
	}
	if cfg.UseLocalSeed {
		recoveryConfig = &dcr.RecoveryCfg{UseLocalSeed: true}
	}

	walletCtx, cancel := context.WithCancel(mainCtx)

	w, err := dcr.CreateWallet(walletCtx, params, recoveryConfig)
	if err != nil {
		cancel()
		return "", err
	}

	wallets[name] = &wallet{
		Wallet:             w,
		log:                logger,
		ctx:                walletCtx,
		cancelCtx:          cancel,
		allowUnsyncedAddrs: cfg.AllowUnsyncedAddrs,
	}
	return "wallet created", nil
}

// CreateWatchOnlyWallet creates a watch-only wallet with the given config JSON.
func CreateWatchOnlyWallet(configJSON string) (string, error) {
	walletsMtx.Lock()
	defer walletsMtx.Unlock()
	if !initialized {
		return "", errors.New("libwallet is not initialized")
	}

	var cfg Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return "", fmt.Errorf("malformed config: %v", err)
	}

	name := cfg.Name
	if _, exists := wallets[name]; exists {
		return "", fmt.Errorf("wallet already exists with name: %q", name)
	}

	logger := logBackend.SubLogger(name)
	params := dcr.CreateWalletParams{
		OpenWalletParams: dcr.OpenWalletParams{
			Net:      cfg.Net,
			DataDir:  cfg.DataDir,
			DbDriver: "bdb",
			Logger:   logger,
		},
	}

	walletCtx, cancel := context.WithCancel(mainCtx)

	w, err := dcr.CreateWatchOnlyWallet(walletCtx, cfg.PubKey, params, cfg.UseLocalSeed)
	if err != nil {
		cancel()
		return "", err
	}

	wallets[name] = &wallet{
		Wallet:             w,
		log:                logger,
		ctx:                walletCtx,
		cancelCtx:          cancel,
		allowUnsyncedAddrs: cfg.AllowUnsyncedAddrs,
	}
	return "wallet created", nil
}

// LoadWallet loads an existing wallet.
func LoadWallet(configJSON string) (string, error) {
	walletsMtx.Lock()
	defer walletsMtx.Unlock()
	if !initialized {
		return "", errors.New("libwallet is not initialized")
	}

	var cfg Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return "", fmt.Errorf("malformed config: %v", err)
	}

	name := cfg.Name
	if _, exists := wallets[name]; exists {
		return "wallet already loaded", nil // not an error, already loaded
	}

	logger := logBackend.SubLogger(name)
	params := dcr.OpenWalletParams{
		Net:      cfg.Net,
		DataDir:  cfg.DataDir,
		DbDriver: "bdb",
		Logger:   logger,
	}

	walletCtx, cancel := context.WithCancel(mainCtx)

	w, err := dcr.LoadWallet(walletCtx, params)
	if err != nil {
		cancel()
		return "", err
	}

	if err = w.OpenWallet(walletCtx); err != nil {
		cancel()
		return "", err
	}

	wallets[name] = &wallet{
		Wallet:             w,
		log:                logger,
		ctx:                walletCtx,
		cancelCtx:          cancel,
		allowUnsyncedAddrs: cfg.AllowUnsyncedAddrs,
	}
	return fmt.Sprintf("wallet %q loaded", name), nil
}

// CloseWallet closes a wallet.
func CloseWallet(name string) (string, error) {
	walletsMtx.Lock()
	defer walletsMtx.Unlock()

	w, exists := wallets[name]
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	w.cancelCtx()
	w.Wait()
	err := w.CloseWallet()
	delete(wallets, name) // Always remove to prevent leaked references
	if err != nil {
		return "", fmt.Errorf("close wallet %q error: %v", name, err)
	}
	return fmt.Sprintf("wallet %q shutdown", name), nil
}

// WalletSeed returns the wallet seed.
func WalletSeed(name, pass string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q not loaded", name)
	}

	seed, err := w.DecryptSeed([]byte(pass))
	if err != nil {
		return "", fmt.Errorf("w.DecryptSeed error: %v", err)
	}

	return seed, nil
}

// WalletBalance returns the wallet balance as JSON.
func WalletBalance(name string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q not loaded", name)
	}

	const confs = 1
	bals, err := w.AccountBalances(w.ctx, confs)
	if err != nil {
		return "", fmt.Errorf("w.AccountBalances error: %v", err)
	}

	balMap := map[string]int64{
		"confirmed":   0,
		"unconfirmed": 0,
	}

	for _, bal := range bals {
		balMap["confirmed"] += int64(bal.Spendable)
		balMap["unconfirmed"] += int64(bal.Total) - int64(bal.Spendable)
	}

	balJSON, err := json.Marshal(balMap)
	if err != nil {
		return "", fmt.Errorf("marshal balMap error: %v", err)
	}

	return string(balJSON), nil
}

// ChangePassphrase changes the wallet passphrase.
func ChangePassphrase(name, oldPass, newPass string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q not loaded", name)
	}

	oldPassBytes, newPassBytes := []byte(oldPass), []byte(newPass)
	if err := w.MainWallet().ChangePrivatePassphrase(w.ctx, oldPassBytes, newPassBytes); err != nil {
		return "", fmt.Errorf("w.ChangePrivatePassphrase error: %v", err)
	}

	if err := w.ReEncryptSeed(oldPassBytes, newPassBytes); err != nil {
		// Undo the passphrase change, since re-encrypting the seed failed.
		if undoErr := w.MainWallet().ChangePrivatePassphrase(w.ctx, newPassBytes, oldPassBytes); undoErr != nil {
			logMtx.RLock()
			log.Errorf("error undoing passphrase change: %v", undoErr)
			logMtx.RUnlock()
			return "", fmt.Errorf("critical failure: seed re-encryption failed (%v) and passphrase rollback failed (%v); wallet may be in an inconsistent state", err, undoErr)
		}
		return "", fmt.Errorf("w.ReEncryptSeed error: %v", err)
	}

	return "passphrase changed", nil
}
