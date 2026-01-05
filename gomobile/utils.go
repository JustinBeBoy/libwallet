package libwallet

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
