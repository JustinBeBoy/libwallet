package libwallet

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	dcrwallet "decred.org/dcrwallet/v4/wallet"
	"decred.org/dcrwallet/v4/wallet/udb"
	"github.com/decred/dcrd/txscript/v4/stdaddr"
	"github.com/decred/libwallet/dcr"
)

// -----------------------------------------------------------------------------
// Address Functions
// -----------------------------------------------------------------------------

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

	addr, err := w.CurrentAddress(udb.DefaultAccountNum)
	if err != nil {
		return "", fmt.Errorf("w.CurrentAddress error: %v", err)
	}

	return addr.String(), nil
}

func SignMessage(name, message, address, password string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q is not loaded", name)
	}

	addr, err := stdaddr.DecodeAddress(address, w.MainWallet().ChainParams())
	if err != nil {
		return "", fmt.Errorf("unable to decode address: %v", err)
	}

	switch addr.(type) {
	case *stdaddr.AddressPubKeyEcdsaSecp256k1V0:
	case *stdaddr.AddressPubKeyHashEcdsaSecp256k1V0:
	default:
		return "", errors.New("invalid address type: must be P2PK or P2PKH")
	}

	if err := w.MainWallet().Unlock(w.ctx, []byte(password), nil); err != nil {
		return "", fmt.Errorf("cannot unlock wallet: %v", err)
	}
	defer w.MainWallet().Lock()

	sig, err := w.MainWallet().SignMessage(w.ctx, message, addr)
	if err != nil {
		return "", fmt.Errorf("unable to sign message: %v", err)
	}

	return base64.StdEncoding.EncodeToString(sig), nil
}

func VerifyMessage(name, message, address, sig string) (string, error) {
	w, ok := loadedWallet(name)
	if !ok {
		return "", fmt.Errorf("wallet with name %q is not loaded", name)
	}

	addr, err := stdaddr.DecodeAddress(address, w.MainWallet().ChainParams())
	if err != nil {
		return "", fmt.Errorf("unable to decode address: %v", err)
	}

	switch addr.(type) {
	case *stdaddr.AddressPubKeyEcdsaSecp256k1V0:
	case *stdaddr.AddressPubKeyHashEcdsaSecp256k1V0:
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

func CreateExtendedKey(createExtKeyJSON string) (string, error) {
	var createExt CreateExtendedKeyReq
	if err := json.Unmarshal([]byte(createExtKeyJSON), &createExt); err != nil {
		return "", fmt.Errorf("malformed create extended key json: %v", err)
	}
	extKey, err := dcr.CreateExtendedKey(
		createExt.Key,
		createExt.ParentKey,
		createExt.ChainCode,
		createExt.Network,
		uint8(createExt.Depth),
		uint32(createExt.ChildN),
		createExt.IsPrivate,
	)
	if err != nil {
		return "", fmt.Errorf("unable to create key: %v", err)
	}
	return extKey, nil
}
