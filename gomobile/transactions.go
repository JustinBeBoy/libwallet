package libwallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	dcrwallet "decred.org/dcrwallet/v4/wallet"
	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/dcrd/txscript/v4/stdaddr"
	"github.com/decred/libwallet/dcr"
)

// -----------------------------------------------------------------------------
// Transaction Functions
// -----------------------------------------------------------------------------

func CreateTransaction(name, createTxReqJSON string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}

	var req CreateTxReq
	if err := json.Unmarshal([]byte(createTxReqJSON), &req); err != nil {
		return "", fmt.Errorf("malformed create transaction request: %v", err)
	}

	outputs := make([]*dcr.Output, len(req.Outputs))
	for i, out := range req.Outputs {
		outputs[i] = &dcr.Output{Address: out.Address, Amount: uint64(out.Amount)}
	}

	inputs := make([]*dcr.Input, len(req.Inputs))
	for i, in := range req.Inputs {
		inputs[i] = &dcr.Input{TxID: in.TxID, Vout: uint32(in.Vout)}
	}

	ignoreInputs := make([]*dcr.Input, len(req.IgnoreInputs))
	for i, in := range req.IgnoreInputs {
		ignoreInputs[i] = &dcr.Input{TxID: in.TxID, Vout: uint32(in.Vout)}
	}

	if req.Sign {
		if err := w.MainWallet().Unlock(w.ctx, []byte(req.Password), nil); err != nil {
			return "", fmt.Errorf("cannot unlock wallet: %v", err)
		}
		defer w.MainWallet().Lock()
	}

	txBytes, txhash, fee, err := w.CreateTransaction(w.ctx, outputs, inputs, ignoreInputs, uint64(req.FeeRate), req.SendAll, req.Sign)
	if err != nil {
		return "", fmt.Errorf("unable to create transaction: %v", err)
	}

	res := &CreateTxRes{
		Hex:  hex.EncodeToString(txBytes),
		Txid: txhash.String(),
		Fee:  int(fee),
	}

	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("unable to marshal create transaction result: %v", err)
	}
	return string(b), nil
}

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
		receiveTime := ltw.TimeReceived
		if ltw.BlockTime != 0 && ltw.BlockTime < ltw.TimeReceived {
			receiveTime = ltw.BlockTime
		}

		var height int64
		if ltw.Confirmations > 0 {
			height = int64(blockHeight) - ltw.Confirmations + 1
		}

		ltRes[i] = &ListTransactionRes{
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
	}

	b, err := json.Marshal(ltRes)
	if err != nil {
		return "", fmt.Errorf("unable to marshal list transactions result: %v", err)
	}
	return string(b), nil
}

func BestBlock(name string) (string, error) {
	w, exists := loadedWallet(name)
	if !exists {
		return "", fmt.Errorf("wallet with name %q does not exist", name)
	}

	blockHash, blockHeight := w.MainWallet().MainChainTip(w.ctx)
	res := &BestBlockRes{Hash: blockHash.String(), Height: int(blockHeight)}

	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("unable to marshal best block result: %v", err)
	}
	return string(b), nil
}

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
		return "", fmt.Errorf("unable to sign tx: %v", err)
	}
	return signedHex, nil
}
