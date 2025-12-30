// Package libwallet exports Decred wallet functionalities for mobile platforms.
// This package is designed to be compiled with gomobile for iOS and Android.
//
// Build examples:
//   gomobile bind -target=android -androidapi=23 -o ./build/libwallet.aar ./mobile
//   gomobile bind -target=ios -o ./build/Libwallet.xcframework ./mobile
package libwallet

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/decred/libwallet/assetlog"
	"github.com/decred/libwallet/dcr"
	"github.com/decred/slog"
)

// -----------------------------------------------------------------------------
// Global variables (shared across files in this package)
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
	_ = logBackend.Close()
	logBackend = nil
	logMtx.Unlock()

	initialized = false
	return "libwallet mobile shutdown", nil
}
