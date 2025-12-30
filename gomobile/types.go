package libwallet

import (
	"context"
	"sync"

	"decred.org/dcrwallet/v4/spv"
	"github.com/decred/libwallet/dcr"
	"github.com/decred/slog"
	"github.com/jrick/logrotate/rotator"
)

// -----------------------------------------------------------------------------
// Public types/constants (used by callers via JSON payloads)
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
	return [...]string{
		"not started",
		"fetching cfilters",
		"fetching headers",
		"discovering addresses",
		"rescanning",
		"sync complete",
	}[ssc]
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
// when generating Objective-C bindings (uint8 maps to 'byte' which conflicts with macOS types).
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

// spv.Notifications is referenced in sync.go; keep import here to ensure
// package compilation includes it when only types are used in a build.
var _ = spv.Notifications{}

// parentLogger wraps slog backend with optional rotator.
type parentLogger struct {
	*slog.Backend
	rotator *rotator.Rotator
	lvl     slog.Level
}
