package libwallet

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"decred.org/dcrwallet/v4/spv"
	dcrwallet "decred.org/dcrwallet/v4/wallet"
)

// -----------------------------------------------------------------------------
// Sync Functions
// -----------------------------------------------------------------------------

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

func SyncWalletStatus(name string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}

	w.syncStatusMtx.RLock()
	ssc, cfh, hh, rh, np := w.syncStatusCode, w.cfiltersHeight, w.headersHeight, w.rescanHeight, w.numPeers
	w.syncStatusMtx.RUnlock()

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
