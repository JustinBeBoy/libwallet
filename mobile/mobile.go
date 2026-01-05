// Package mobile exports Decred wallet functionalities for mobile platforms.
// This package is designed to be compiled with gomobile for iOS and Android.
//
// Build cmd: gomobile bind -target=ios -o ./build/Libwallet.xcframework ./mobile
package mobile

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	dexmnemonic "decred.org/dcrdex/client/mnemonic"
	"decred.org/dcrwallet/v4/spv"
	dcrwallet "decred.org/dcrwallet/v4/wallet"
	"decred.org/dcrwallet/v4/wallet/udb"
	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/dcrd/txscript/v4/stdaddr"
	"github.com/decred/libwallet/assetlog"
	"github.com/decred/libwallet/dcr"
	"github.com/decred/libwallet/mnemonic"
	"github.com/decred/slog"
	"github.com/jrick/logrotate/rotator"
)

// -----------------------------------------------------------------------------
// Global variables
// -----------------------------------------------------------------------------

var (
	mainCtx       context.Context
	cancelMainCtx context.CancelFunc
	wg            sync.WaitGroup

	logBackend *parentLogger
	logMtx     sync.RWMutex
	log        slog.Logger

	// walletsMtx protects wallets and initialized.
	walletsMtx  sync.RWMutex
	wallets     = make(map[string]*wallet)
	initialized bool
)

// -----------------------------------------------------------------------------
// Types
// -----------------------------------------------------------------------------

const (
	// ErrCodeNotSynced is returned when the wallet must be synced to perform an
	// action but is not.
	ErrCodeNotSynced = 1

	defaultAccount = "default"
)

// SyncStatusCode represents the sync status of a wallet.
type SyncStatusCode int

const (
	SSCNotStarted SyncStatusCode = iota
	SSCFetchingCFilters
	SSCFetchingHeaders
	SSCDiscoveringAddrs
	SSCRescanning
	SSCComplete
)

func (ssc SyncStatusCode) String() string {
	return [...]string{"not started", "fetching cfilters", "fetching headers",
		"discovering addresses", "rescanning", "sync complete"}[ssc]
}

// SyncStatusRes represents the sync status response.
type SyncStatusRes struct {
	SyncStatusCode int    `json:"syncstatuscode"`
	SyncStatus     string `json:"syncstatus"`
	TargetHeight   int    `json:"targetheight"`
	NumPeers       int    `json:"numpeers"`
	CFiltersHeight int    `json:"cfiltersheight,omitempty"`
	HeadersHeight  int    `json:"headersheight,omitempty"`
	RescanHeight   int    `json:"rescanheight,omitempty"`
}

// Input represents a transaction input.
type Input struct {
	TxID string `json:"txid"`
	Vout int    `json:"vout"`
}

// Output represents a transaction output.
type Output struct {
	Address string `json:"address"`
	Amount  int    `json:"amount"`
}

// CreateTxReq represents a create transaction request.
type CreateTxReq struct {
	Outputs      []Output `json:"outputs"`
	Inputs       []Input  `json:"inputs"`
	IgnoreInputs []Input  `json:"ignoreinputs"`
	FeeRate      int      `json:"feerate"`
	SendAll      bool     `json:"sendall"`
	Password     string   `json:"password"`
	Sign         bool     `json:"sign"`
}

// CreateTxRes represents a create transaction response.
type CreateTxRes struct {
	Hex  string `json:"hex"`
	Txid string `json:"txid"`
	Fee  int    `json:"fee"`
}

// BestBlockRes represents the best block response.
type BestBlockRes struct {
	Hash   string `json:"hash"`
	Height int    `json:"height"`
}

// ListTransactionRes represents a list transaction response.
type ListTransactionRes struct {
	Address       string   `json:"address,omitempty"`
	Amount        float64  `json:"amount"`
	Category      string   `json:"category"`
	Confirmations int64    `json:"confirmations"`
	Height        int64    `json:"height"`
	Fee           *float64 `json:"fee,omitempty"`
	Time          int64    `json:"time"`
	TxID          string   `json:"txid"`
	Vout          uint32   `json:"vout"`
}

// BirthdayStateRes represents the birthday state response.
type BirthdayStateRes struct {
	Hash          string `json:"hash"`
	Height        uint32 `json:"height"`
	Time          int64  `json:"time"`
	SetFromHeight bool   `json:"setfromheight"`
	SetFromTime   bool   `json:"setfromtime"`
}

// AddressesRes represents the addresses response.
type AddressesRes struct {
	Used   []string `json:"used"`
	Unused []string `json:"unused"`
	Index  uint32   `json:"index"`
}

// Config represents the wallet configuration.
type Config struct {
	Name string `json:"name"`
	// Allow getting unused addresses when not synced.
	AllowUnsyncedAddrs bool   `json:"unsyncedaddrs"`
	Net                string `json:"net"`
	DataDir            string `json:"datadir"`
	// Only needed during creation.
	Birthday int64  `json:"birthday"`
	Pass     string `json:"pass"`
	Mnemonic string `json:"mnemonic"`
	SeedPass string `json:"seedpass"`
	// If the wallet existed before but the db was deleted to reduce
	// storage, restore from the local encrypted seed using the provided
	// password. Also works for watching only wallets with no password.
	UseLocalSeed bool `json:"uselocalseed"`
	// Only needed during watching only creation.
	PubKey string `json:"pubkey"`
}

// AddrFromExtKey represents the address from extended key request.
type AddrFromExtKey struct {
	Key  string `json:"key"`
	Path string `json:"path"`
	// Currently support types: P2PKH
	AddrType         string `json:"addrtype"`
	UseChildBIP32Std bool   `json:"usechildbip32std"`
}

// CreateExtendedKeyReq represents the create extended key request.
// Note: Depth uses int instead of uint8 because gomobile doesn't handle uint8 well
// when generating Objective-C bindings (uint8 maps to 'byte' which conflicts with macOS types)
type CreateExtendedKeyReq struct {
	Key       string `json:"key"`
	ParentKey string `json:"parentkey"`
	ChainCode string `json:"chaincode"`
	Network   string `json:"network"`
	Depth     int    `json:"depth"`
	ChildN    int    `json:"childn"`
	IsPrivate bool   `json:"isprivate"`
}

// -----------------------------------------------------------------------------
// Internal types
// -----------------------------------------------------------------------------

type wallet struct {
	*dcr.Wallet
	log slog.Logger

	sync.WaitGroup
	ctx       context.Context
	cancelCtx context.CancelFunc

	syncStatusMtx                                                       sync.RWMutex
	syncStatusCode                                                      SyncStatusCode
	targetHeight, cfiltersHeight, headersHeight, rescanHeight, numPeers int
	rescanning, allowUnsyncedAddrs                                      bool
}

type parentLogger struct {
	*slog.Backend
	rotator *rotator.Rotator
	lvl     slog.Level
}

func newParentLogger(rotator *rotator.Rotator, lvl slog.Level) *parentLogger {
	return &parentLogger{
		Backend: slog.NewBackend(rotator),
		rotator: rotator,
		lvl:     lvl,
	}
}

func newParentStdOutLogger(lvl slog.Level) *parentLogger {
	backend := slog.NewBackend(os.Stdout)
	return &parentLogger{
		Backend: backend,
		lvl:     lvl,
	}
}

func (pl *parentLogger) SubLogger(name string) slog.Logger {
	logger := pl.Logger(name)
	logger.SetLevel(pl.lvl)
	return logger
}

func (pl *parentLogger) Close() error {
	if pl.rotator != nil {
		return pl.rotator.Close()
	}
	return nil
}

// -----------------------------------------------------------------------------
// Helper functions
// -----------------------------------------------------------------------------

func loadedWallet(name string) (*wallet, bool) {
	walletsMtx.RLock()
	defer walletsMtx.RUnlock()

	w, ok := wallets[name]
	if !ok {
		logMtx.RLock()
		if log != nil {
			log.Debugf("attempted to use an unloaded wallet %q", name)
		}
		logMtx.RUnlock()
	}
	return w, ok
}

// -----------------------------------------------------------------------------
// Core Functions
// -----------------------------------------------------------------------------

// Initialize initializes the libwallet mobile library.
func Initialize(logDir, logLvl string) (string, error) {
	walletsMtx.Lock()
	defer walletsMtx.Unlock()
	if initialized {
		return "", errors.New("duplicate initialization")
	}

	lvl, ok := slog.LevelFromString(logLvl)
	if !ok {
		return "", fmt.Errorf("unknown log level %q", logLvl)
	}

	if logDir != "" {
		logSpinner, err := assetlog.NewRotator(logDir, "dcrwallet.log")
		if err != nil {
			return "", fmt.Errorf("error initializing log rotator: %v", err)
		}

		logBackend = newParentLogger(logSpinner, lvl)
		err = dcr.InitGlobalLogging(logDir, logBackend, lvl)
		if err != nil {
			return "", fmt.Errorf("error initializing logger for external pkgs: %v", err)
		}
	} else {
		logBackend = newParentStdOutLogger(lvl)
	}

	logMtx.Lock()
	log = logBackend.SubLogger("APP")
	logMtx.Unlock()

	mainCtx, cancelMainCtx = context.WithCancel(context.Background())

	initialized = true
	return "libwallet mobile initialized", nil
}

// Shutdown shuts down the libwallet mobile library.
func Shutdown() (string, error) {
	walletsMtx.Lock()
	defer walletsMtx.Unlock()
	if !initialized {
		return "", errors.New("not initialized")
	}

	logMtx.RLock()
	log.Debug("libwallet mobile shutting down")
	logMtx.RUnlock()

	for _, w := range wallets {
		if err := w.CloseWallet(); err != nil {
			w.log.Errorf("close wallet error: %v", err)
		}
	}
	wallets = make(map[string]*wallet)

	// Stop all remaining background processes and wait for them to stop.
	cancelMainCtx()
	wg.Wait()

	// Close the logger backend as the last step.
	logMtx.Lock()
	log.Debug("libwallet mobile shutdown")
	logBackend.Close()
	logBackend = nil
	logMtx.Unlock()

	initialized = false
	return "libwallet mobile shutdown", nil
}

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
			DbDriver: "bdb", // use badgerdb for mobile!
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
		recoveryConfig = &dcr.RecoveryCfg{
			UseLocalSeed: true,
		}
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
		DbDriver: "bdb", // use badgerdb for mobile!
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
	if err := w.CloseWallet(); err != nil {
		return "", fmt.Errorf("close wallet %q error: %v", name, err)
	}
	delete(wallets, name)
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
		// Undo the passphrase change, since the re-encrypting the seed failed.
		if undoErr := w.MainWallet().ChangePrivatePassphrase(w.ctx, newPassBytes, oldPassBytes); undoErr != nil {
			logMtx.RLock()
			log.Errorf("error undoing passphrase change: %v", undoErr)
			logMtx.RUnlock()
		}
		return "", fmt.Errorf("w.ReEncryptSeed error: %v", err)
	}

	return "passphrase changed", nil
}

// -----------------------------------------------------------------------------
// Address Functions
// -----------------------------------------------------------------------------

// CurrentReceiveAddress returns the current receive address.
func CurrentReceiveAddress(name string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q is not loaded", name)
	}

	if !w.allowUnsyncedAddrs {
		synced, _ := w.IsSynced(w.ctx)
		if !synced {
			return "", fmt.Errorf("currentReceiveAddress requested on an unsynced wallet (error code: %d)", ErrCodeNotSynced)
		}
	}

	addr, err := w.CurrentAddress(udb.DefaultAccountNum)
	if err != nil {
		return "", fmt.Errorf("w.CurrentAddress error: %v", err)
	}

	return addr.String(), nil
}

// NewExternalAddress creates a new external address.
func NewExternalAddress(name string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q is not loaded", name)
	}

	if !w.allowUnsyncedAddrs {
		synced, _ := w.IsSynced(w.ctx)
		if !synced {
			return "", fmt.Errorf("newExternalAddress requested on an unsynced wallet (error code: %d)", ErrCodeNotSynced)
		}
	}

	_, err := w.NewExternalAddress(w.ctx, udb.DefaultAccountNum)
	if err != nil {
		return "", fmt.Errorf("w.NewExternalAddress error: %v", err)
	}

	// NewExternalAddress will take the current address before increasing
	// the index. Get the current address after increasing the index.
	addr, err := w.CurrentAddress(udb.DefaultAccountNum)
	if err != nil {
		return "", fmt.Errorf("w.CurrentAddress error: %v", err)
	}

	return addr.String(), nil
}

// SignMessage signs a message with the private key of the address.
func SignMessage(name, message, address, password string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q is not loaded", name)
	}

	addr, err := stdaddr.DecodeAddress(address, w.MainWallet().ChainParams())
	if err != nil {
		return "", fmt.Errorf("unable to decode address: %v", err)
	}

	// Addresses must have an associated secp256k1 private key and therefore
	// must be P2PK or P2PKH (P2SH is not allowed).
	switch addr.(type) {
	case *stdaddr.AddressPubKeyEcdsaSecp256k1V0:
	case *stdaddr.AddressPubKeyHashEcdsaSecp256k1V0:
		// Valid address types, proceed to sign.
	default:
		return "", errors.New("invalid address type: must be P2PK or P2PKH")
	}

	if err := w.MainWallet().Unlock(w.ctx, []byte(password), nil); err != nil {
		return "", fmt.Errorf("cannot unlock wallet: %v", err)
	}

	sig, err := w.MainWallet().SignMessage(w.ctx, message, addr)
	if err != nil {
		return "", fmt.Errorf("unable to sign message: %v", err)
	}

	sEnc := base64.StdEncoding.EncodeToString(sig)
	return sEnc, nil
}

// VerifyMessage verifies a signed message.
func VerifyMessage(name, message, address, sig string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q is not loaded", name)
	}

	addr, err := stdaddr.DecodeAddress(address, w.MainWallet().ChainParams())
	if err != nil {
		return "", fmt.Errorf("unable to decode address: %v", err)
	}

	// Addresses must have an associated secp256k1 private key and therefore
	// must be P2PK or P2PKH (P2SH is not allowed).
	switch addr.(type) {
	case *stdaddr.AddressPubKeyEcdsaSecp256k1V0:
	case *stdaddr.AddressPubKeyHashEcdsaSecp256k1V0:
		// Valid address types, proceed with verification.
	default:
		return "", errors.New("invalid address type: must be P2PK or P2PKH")
	}

	sigBytes, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return "", fmt.Errorf("unable to decode signature: %v", err)
	}

	ok, err = dcrwallet.VerifyMessage(message, addr, sigBytes, w.MainWallet().ChainParams())
	if err != nil {
		return "", fmt.Errorf("unable to verify message: %v", err)
	}

	return fmt.Sprintf("%v", ok), nil
}

// Addresses returns the used and unused addresses.
func Addresses(name, nUsed, nUnused string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q is not loaded", name)
	}

	nUsedVal, err := strconv.ParseUint(nUsed, 10, 32)
	if err != nil {
		return "", fmt.Errorf("number of used addresses is not a uint32: %v", err)
	}

	nUnusedVal, err := strconv.ParseUint(nUnused, 10, 32)
	if err != nil {
		return "", fmt.Errorf("number of unused addresses is not a uint32: %v", err)
	}

	used, unused, index, err := w.DefaultAccountAddresses(w.ctx, uint32(nUsedVal), uint32(nUnusedVal))
	if err != nil {
		return "", fmt.Errorf("w.DefaultAccountAddresses error: %v", err)
	}

	res := &AddressesRes{
		Used:   used,
		Unused: []string{},
		Index:  index,
	}
	synced, _ := w.IsSynced(w.ctx)
	if synced || w.allowUnsyncedAddrs {
		res.Unused = unused
	}

	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("unable to marshal addresses: %v", err)
	}

	return string(b), nil
}

// DefaultPubkey returns the default account public key.
func DefaultPubkey(name string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q is not loaded", name)
	}

	pubkey, err := w.AccountPubkey(w.ctx, defaultAccount)
	if err != nil {
		return "", fmt.Errorf("unable to get default pubkey: %v", err)
	}

	return pubkey, nil
}

// ValidateAddr validates an address.
func ValidateAddr(name, addr string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	validated, err := w.ValidateAddr(w.ctx, addr)
	if err != nil {
		return "", fmt.Errorf("unable to validate address: %v", err)
	}
	b, err := json.Marshal(validated)
	if err != nil {
		return "", fmt.Errorf("unable to marshal validate address: %v", err)
	}
	return string(b), nil
}

// AddrFromExtendedKey returns an address from an extended key.
func AddrFromExtendedKey(addrFromExtKeyJSON string) (string, error) {
	var fromExt AddrFromExtKey
	if err := json.Unmarshal([]byte(addrFromExtKeyJSON), &fromExt); err != nil {
		return "", fmt.Errorf("malformed create addr json: %v", err)
	}
	addr, err := dcr.AddrFromExtendedKey(fromExt.Key, fromExt.Path, fromExt.AddrType, fromExt.UseChildBIP32Std)
	if err != nil {
		return "", fmt.Errorf("unable to create address: %v", err)
	}
	return addr, nil
}

// CreateExtendedKey creates an extended key.
func CreateExtendedKey(createExtKeyJSON string) (string, error) {
	var createExt CreateExtendedKeyReq
	if err := json.Unmarshal([]byte(createExtKeyJSON), &createExt); err != nil {
		return "", fmt.Errorf("malformed create extended key json: %v", err)
	}
	extKey, err := dcr.CreateExtendedKey(createExt.Key, createExt.ParentKey, createExt.ChainCode,
		createExt.Network, uint8(createExt.Depth), uint32(createExt.ChildN), createExt.IsPrivate)
	if err != nil {
		return "", fmt.Errorf("unable to create key: %v", err)
	}
	return extKey, nil
}

// -----------------------------------------------------------------------------
// Sync Functions
// -----------------------------------------------------------------------------

// SyncWallet starts syncing the wallet with the given peers.
func SyncWallet(name, peers string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	var peerList []string
	for _, p := range strings.Split(peers, ",") {
		if p = strings.TrimSpace(p); p != "" {
			peerList = append(peerList, p)
		}
	}
	ntfns := &spv.Notifications{
		Synced: func(sync bool) {
			w.syncStatusMtx.Lock()
			w.syncStatusCode = SSCComplete
			w.syncStatusMtx.Unlock()
			w.log.Debug("Sync completed.")
		},
		PeerConnected: func(peerCount int32, addr string) {
			w.syncStatusMtx.Lock()
			w.numPeers = int(peerCount)
			w.syncStatusMtx.Unlock()
			w.log.Debugf("Connected to peer at %s. %d total peers.", addr, peerCount)
		},
		PeerDisconnected: func(peerCount int32, addr string) {
			w.syncStatusMtx.Lock()
			w.numPeers = int(peerCount)
			w.syncStatusMtx.Unlock()
			w.log.Debugf("Disconnected from peer at %s. %d total peers.", addr, peerCount)
		},
		FetchMissingCFiltersStarted: func() {
			w.syncStatusMtx.Lock()
			if w.rescanning {
				w.syncStatusMtx.Unlock()
				return
			}
			w.syncStatusCode = SSCFetchingCFilters
			w.syncStatusMtx.Unlock()
			w.log.Debug("Fetching missing cfilters started.")
		},
		FetchMissingCFiltersProgress: func(startCFiltersHeight, endCFiltersHeight int32) {
			w.syncStatusMtx.Lock()
			w.cfiltersHeight = int(endCFiltersHeight)
			w.syncStatusMtx.Unlock()
			w.log.Debugf("Fetching cfilters from %d to %d.", startCFiltersHeight, endCFiltersHeight)
		},
		FetchMissingCFiltersFinished: func() {
			w.syncStatusMtx.Lock()
			w.cfiltersHeight = w.targetHeight
			w.syncStatusMtx.Unlock()
			w.log.Debug("Finished fetching missing cfilters.")
		},
		FetchHeadersStarted: func() {
			w.syncStatusMtx.Lock()
			if w.rescanning {
				w.syncStatusMtx.Unlock()
				return
			}
			w.syncStatusCode = SSCFetchingHeaders
			w.syncStatusMtx.Unlock()
			w.log.Debug("Fetching headers started.")
		},
		FetchHeadersProgress: func(lastHeaderHeight int32, lastHeaderTime int64) {
			w.syncStatusMtx.Lock()
			w.headersHeight = int(lastHeaderHeight)
			w.syncStatusMtx.Unlock()
			w.log.Debugf("Fetching headers to %d.", lastHeaderHeight)
		},
		FetchHeadersFinished: func() {
			w.syncStatusMtx.Lock()
			w.headersHeight = w.targetHeight
			w.syncStatusMtx.Unlock()
			w.log.Debug("Fetching headers finished.")
		},
		DiscoverAddressesStarted: func() {
			w.syncStatusMtx.Lock()
			if w.rescanning {
				w.syncStatusMtx.Unlock()
				return
			}
			w.syncStatusCode = SSCDiscoveringAddrs
			w.syncStatusMtx.Unlock()
			w.log.Debug("Discover addresses started.")
		},
		DiscoverAddressesFinished: func() {
			w.log.Debug("Discover addresses finished.")
		},
		RescanStarted: func() {
			w.syncStatusMtx.Lock()
			if w.rescanning {
				w.syncStatusMtx.Unlock()
				return
			}
			w.syncStatusCode = SSCRescanning
			w.syncStatusMtx.Unlock()
			w.log.Debug("Rescan started.")
		},
		RescanProgress: func(rescannedThrough int32) {
			w.syncStatusMtx.Lock()
			w.rescanHeight = int(rescannedThrough)
			w.syncStatusMtx.Unlock()
			w.log.Debugf("Rescanned through block %d.", rescannedThrough)
		},
		RescanFinished: func() {
			w.syncStatusMtx.Lock()
			w.rescanHeight = w.targetHeight
			w.syncStatusMtx.Unlock()
			w.log.Debug("Rescan finished.")
		},
	}
	if err := w.StartSync(w.ctx, ntfns, peerList...); err != nil {
		return "", err
	}
	return "sync started", nil
}

// SyncWalletStatus returns the sync status of the wallet.
func SyncWalletStatus(name string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}

	w.syncStatusMtx.RLock()
	var ssc, cfh, hh, rh, np = w.syncStatusCode, w.cfiltersHeight, w.headersHeight, w.rescanHeight, w.numPeers
	w.syncStatusMtx.RUnlock()

	// Sometimes it appears we miss a notification during start up. This is
	// a bandaid to put us as synced in that case.
	synced, targetHeight := w.IsSynced(w.ctx)
	w.syncStatusMtx.Lock()
	if ssc != SSCComplete && synced && !w.rescanning {
		ssc = SSCComplete
		w.syncStatusCode = ssc
	}
	w.syncStatusMtx.Unlock()

	ss := &SyncStatusRes{
		SyncStatusCode: int(ssc),
		SyncStatus:     ssc.String(),
		TargetHeight:   int(targetHeight),
		NumPeers:       np,
	}
	switch ssc {
	case SSCFetchingCFilters:
		ss.CFiltersHeight = cfh
	case SSCFetchingHeaders:
		ss.HeadersHeight = hh
	case SSCRescanning:
		ss.RescanHeight = rh
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return "", fmt.Errorf("unable to marshal sync status result: %v", err)
	}
	return string(b), nil
}

// RescanFromHeight starts a rescan from the given height.
func RescanFromHeight(name, height string) (string, error) {
	heightVal, err := strconv.ParseUint(height, 10, 32)
	if err != nil {
		return "", fmt.Errorf("height is not an uint32: %v", err)
	}
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	synced, _ := w.IsSynced(w.ctx)
	if !synced {
		return "", fmt.Errorf("rescanFromHeight requested on an unsynced wallet (error code: %d)", ErrCodeNotSynced)
	}
	w.syncStatusMtx.Lock()
	if w.rescanning {
		w.syncStatusMtx.Unlock()
		return "", fmt.Errorf("wallet %q already rescanning", name)
	}
	w.syncStatusCode = SSCRescanning
	w.rescanning = true
	w.rescanHeight = int(heightVal)
	w.syncStatusMtx.Unlock()
	w.Add(1)
	go func() {
		defer func() {
			w.syncStatusMtx.Lock()
			w.syncStatusCode = SSCComplete
			w.rescanning = false
			w.syncStatusMtx.Unlock()
			w.Done()
		}()
		prog := make(chan dcrwallet.RescanProgress)
		go func() {
			w.RescanProgressFromHeight(w.ctx, int32(heightVal), prog)
		}()
		for {
			select {
			case p, open := <-prog:
				if !open {
					return
				}
				if p.Err != nil {
					logMtx.RLock()
					log.Errorf("rescan wallet %q error: %v", name, p.Err)
					logMtx.RUnlock()
					return
				}
				w.syncStatusMtx.Lock()
				w.rescanHeight = int(p.ScannedThrough)
				w.syncStatusMtx.Unlock()
			case <-w.ctx.Done():
				return
			}
		}
	}()
	return fmt.Sprintf("rescan from height %d for wallet %q started", heightVal, name), nil
}

// BirthState returns the birthday state of the wallet.
func BirthState(name string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q is not loaded", name)
	}

	bs, err := w.MainWallet().BirthState(w.ctx)
	if err != nil {
		return "", fmt.Errorf("wallet.BirthState error: %v", err)
	}
	if bs == nil {
		return "", fmt.Errorf("birth state is nil for wallet %q", name)
	}

	bsRes := &BirthdayStateRes{
		Hash:          bs.Hash.String(),
		Height:        bs.Height,
		Time:          bs.Time.Unix(),
		SetFromHeight: bs.SetFromHeight,
		SetFromTime:   bs.SetFromTime,
	}
	b, err := json.Marshal(bsRes)
	if err != nil {
		return "", fmt.Errorf("unable to marshal birth state result: %v", err)
	}
	return string(b), nil
}

// -----------------------------------------------------------------------------
// Transaction Functions
// -----------------------------------------------------------------------------

// CreateTransaction creates a transaction.
func CreateTransaction(name, createTxReqJSON string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	var req CreateTxReq
	if err := json.Unmarshal([]byte(createTxReqJSON), &req); err != nil {
		return "", fmt.Errorf("malformed sign send request: %v", err)
	}

	outputs := make([]*dcr.Output, len(req.Outputs))
	for i, out := range req.Outputs {
		o := &dcr.Output{
			Address: out.Address,
			Amount:  uint64(out.Amount),
		}
		outputs[i] = o
	}

	inputs := make([]*dcr.Input, len(req.Inputs))
	for i, in := range req.Inputs {
		o := &dcr.Input{
			TxID: in.TxID,
			Vout: uint32(in.Vout),
		}
		inputs[i] = o
	}

	ignoreInputs := make([]*dcr.Input, len(req.IgnoreInputs))
	for i, in := range req.IgnoreInputs {
		o := &dcr.Input{
			TxID: in.TxID,
			Vout: uint32(in.Vout),
		}
		ignoreInputs[i] = o
	}

	if req.Sign {
		if err := w.MainWallet().Unlock(w.ctx, []byte(req.Password), nil); err != nil {
			return "", fmt.Errorf("cannot unlock wallet: %v", err)
		}
		defer w.MainWallet().Lock()
	}

	txBytes, txhash, fee, err := w.CreateTransaction(w.ctx, outputs, inputs, ignoreInputs, uint64(req.FeeRate), req.SendAll, req.Sign)
	if err != nil {
		return "", fmt.Errorf("unable to sign send transaction: %v", err)
	}
	res := &CreateTxRes{
		Hex:  hex.EncodeToString(txBytes),
		Txid: txhash.String(),
		Fee:  int(fee),
	}

	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("unable to marshal sign send transaction result: %v", err)
	}
	return string(b), nil
}

// SendRawTransaction sends a raw transaction.
func SendRawTransaction(name, txHex string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	txHash, err := w.SendRawTransaction(w.ctx, txHex)
	if err != nil {
		return "", fmt.Errorf("unable to send raw transaction: %v", err)
	}
	return txHash.String(), nil
}

// ListUnspents returns the unspent outputs.
func ListUnspents(name string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	res, err := w.MainWallet().ListUnspent(w.ctx, 1, math.MaxInt32, nil, defaultAccount)
	if err != nil {
		return "", fmt.Errorf("unable to get unspents: %v", err)
	}

	type ListUnspentRes struct {
		TxID          string  `json:"txid"`
		Vout          uint32  `json:"vout"`
		Tree          int8    `json:"tree"`
		TxType        int     `json:"txtype"`
		Address       string  `json:"address"`
		Account       string  `json:"account"`
		ScriptPubKey  string  `json:"scriptPubKey"`
		RedeemScript  string  `json:"redeemScript,omitempty"`
		Amount        float64 `json:"amount"`
		Confirmations int64   `json:"confirmations"`
		Spendable     bool    `json:"spendable"`
		IsChange      bool    `json:"ischange"`
	}

	// Add is change to results.
	unspentRes := make([]ListUnspentRes, len(res))
	for i, unspent := range res {
		addr, err := stdaddr.DecodeAddress(unspent.Address, w.MainWallet().ChainParams())
		if err != nil {
			return "", fmt.Errorf("unable to decode address: %v", err)
		}

		ka, err := w.MainWallet().KnownAddress(w.ctx, addr)
		if err != nil {
			return "", fmt.Errorf("unspent address is not known: %v", err)
		}

		isChange := false
		if ka, ok := ka.(dcrwallet.BIP0044Address); ok {
			_, branch, _ := ka.Path()
			isChange = branch == 1
		}
		unspentRes[i] = ListUnspentRes{
			TxID:          unspent.TxID,
			Vout:          unspent.Vout,
			Tree:          unspent.Tree,
			TxType:        unspent.TxType,
			Address:       unspent.Address,
			Account:       unspent.Account,
			ScriptPubKey:  unspent.ScriptPubKey,
			RedeemScript:  unspent.RedeemScript,
			Amount:        unspent.Amount,
			Confirmations: unspent.Confirmations,
			Spendable:     unspent.Spendable,
			IsChange:      isChange,
		}
	}
	b, err := json.Marshal(unspentRes)
	if err != nil {
		return "", fmt.Errorf("unable to marshal list unspents result: %v", err)
	}
	return string(b), nil
}

// EstimateFee estimates the fee for a transaction.
func EstimateFee(name, nBlocks string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	nBlocksVal, err := strconv.ParseUint(nBlocks, 10, 64)
	if err != nil {
		return "", fmt.Errorf("number of blocks is not a uint64: %v", err)
	}
	txFee, err := w.FetchFeeFromOracle(w.ctx, nBlocksVal)
	if err != nil {
		return "", fmt.Errorf("unable to get fee from oracle: %v", err)
	}
	return fmt.Sprintf("%d", uint64(txFee*1e8)), nil
}

// ListTransactions lists transactions.
func ListTransactions(name, from, count string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	fromVal, err := strconv.ParseInt(from, 10, 32)
	if err != nil {
		return "", fmt.Errorf("from is not an int: %v", err)
	}
	countVal, err := strconv.ParseInt(count, 10, 32)
	if err != nil {
		return "", fmt.Errorf("count is not an int: %v", err)
	}
	res, err := w.MainWallet().ListTransactions(w.ctx, int(fromVal), int(countVal))
	if err != nil {
		return "", fmt.Errorf("unable to get transactions: %v", err)
	}
	_, blockHeight := w.MainWallet().MainChainTip(w.ctx)
	ltRes := make([]*ListTransactionRes, len(res))
	for i, ltw := range res {
		// Use earliest of receive time or block time if the transaction is mined.
		receiveTime := ltw.TimeReceived
		if ltw.BlockTime != 0 && ltw.BlockTime < ltw.TimeReceived {
			receiveTime = ltw.BlockTime
		}

		var height int64
		if ltw.Confirmations > 0 {
			height = int64(blockHeight) - ltw.Confirmations + 1
		}

		lt := &ListTransactionRes{
			Address:       ltw.Address,
			Amount:        ltw.Amount,
			Category:      ltw.Category,
			Confirmations: ltw.Confirmations,
			Height:        height,
			Fee:           ltw.Fee,
			Time:          receiveTime,
			TxID:          ltw.TxID,
			Vout:          ltw.Vout,
		}
		ltRes[i] = lt
	}
	b, err := json.Marshal(ltRes)
	if err != nil {
		return "", fmt.Errorf("unable to marshal list transactions result: %v", err)
	}
	return string(b), nil
}

// BestBlock returns the best block.
func BestBlock(name string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	blockHash, blockHeight := w.MainWallet().MainChainTip(w.ctx)
	res := &BestBlockRes{
		Hash:   blockHash.String(),
		Height: int(blockHeight),
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("unable to marshal best block result: %v", err)
	}
	return string(b), nil
}

// DecodeTx decodes a transaction.
func DecodeTx(name, txHex string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	decoded, err := w.DecodeTx(txHex)
	if err != nil {
		return "", fmt.Errorf("unable to decode tx: %v", err)
	}
	b, err := json.Marshal(decoded)
	if err != nil {
		return "", fmt.Errorf("unable to marshal decoded tx: %v", err)
	}
	return string(b), nil
}

// GetTxn gets transactions by hashes.
func GetTxn(name, hashesJSON string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	var txIDs []string
	if err := json.Unmarshal([]byte(hashesJSON), &txIDs); err != nil {
		return "", fmt.Errorf("unable to unmarshal hashes: %v", err)
	}
	txHashes := make([]*chainhash.Hash, len(txIDs))
	for i, txID := range txIDs {
		txHash, err := chainhash.NewHashFromStr(txID)
		if err != nil {
			return "", fmt.Errorf("unable to create tx hash: %v", err)
		}
		txHashes[i] = txHash
	}
	hexes, err := w.GetTxn(w.ctx, txHashes)
	if err != nil {
		return "", fmt.Errorf("unable to get txn: %v", err)
	}
	b, err := json.Marshal(hexes)
	if err != nil {
		return "", fmt.Errorf("unable to marshal txn: %v", err)
	}
	return string(b), nil
}

// AddSigs adds signatures to a transaction.
func AddSigs(name, txHex, sigScriptsJSON string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}
	var sigScripts []string
	if err := json.Unmarshal([]byte(sigScriptsJSON), &sigScripts); err != nil {
		return "", fmt.Errorf("unable to unmarshal sig scripts: %v", err)
	}
	signedHex, err := w.AddSigs(txHex, sigScripts)
	if err != nil {
		return "", fmt.Errorf("unable sign tx: %v", err)
	}
	return signedHex, nil
}
