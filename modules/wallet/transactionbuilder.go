package wallet

import (
	"bytes"
	"log"
	"sort"

	"github.com/NebulousLabs/Sia/crypto"
	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/types"
)

// spendableWallet is a wrapper over Wallet that implements WalletSpender.
type spendableWallet struct {
	wallet *Wallet
	lockID int
}

func (w *spendableWallet) AcquireLock() {
	w.lockID = w.wallet.mu.Lock()
}

func (w *spendableWallet) ReleaseLock() {
	w.wallet.mu.Unlock(w.lockID)
}

func (w *spendableWallet) SiacoinOutputs() map[types.SiacoinOutputID]types.SiacoinOutput {
	return w.wallet.siacoinOutputs
}

func (w *spendableWallet) SiafundOutputs() map[types.SiafundOutputID]types.SiafundOutput {
	return w.wallet.siafundOutputs
}

func (w *spendableWallet) SpendHeight(oid types.OutputID) types.BlockHeight {
	return w.wallet.spentOutputs[oid]
}

func (w *spendableWallet) SetSpendHeight(oid types.OutputID, height types.BlockHeight) {
	w.wallet.spentOutputs[oid] = height
}

func (w *spendableWallet) RemoveOutput(oid types.OutputID) {
	delete(w.wallet.spentOutputs, oid)
}

func (w *spendableWallet) UnconfirmedProcessedTransactions() []modules.ProcessedTransaction {
	return w.wallet.unconfirmedProcessedTransactions
}

func (w *spendableWallet) Key(hash types.UnlockHash) types.SpendableKey {
	return w.wallet.keys[hash]
}

func (w *spendableWallet) HasKey(hash types.UnlockHash) bool {
	_, exists := w.wallet.keys[hash]
	return exists
}

func (w *spendableWallet) ConsensusSetHeight() types.BlockHeight {
	return w.wallet.consensusSetHeight
}

func (w *spendableWallet) NextPrimarySeedAddress() (types.UnlockConditions, error) {
	return w.wallet.nextPrimarySeedAddress()
}

// transactionBuilder allows transactions to be manually constructed, including
// the ability to fund transactions with siacoins and siafunds from the wallet.
type transactionBuilder struct {
	parents       []types.Transaction
	transaction   types.Transaction
	siacoinInputs []int
	siafundInputs []int

	walletSpender modules.WalletSpender
}

// addSignatures will sign a transaction using a spendable key, with support
// for multisig spendable keys. Because of the restricted input, the function
// is compatible with both siacoin inputs and siafund inputs.
func addSignatures(txn *types.Transaction, cf types.CoveredFields, uc types.UnlockConditions, parentID crypto.Hash, spendKey types.SpendableKey) error {
	// Try to find the matching secret key for each public key - some public
	// keys may not have a match. Some secret keys may be used multiple times,
	// which is why public keys are used as the outer loop.
	totalSignatures := uint64(0)
	for i, siaPubKey := range uc.PublicKeys {
		// Search for the matching secret key to the public key.
		for j := range spendKey.SecretKeys {
			pubKey := spendKey.SecretKeys[j].PublicKey()
			if bytes.Compare(siaPubKey.Key, pubKey[:]) != 0 {
				continue
			}

			// Found the right secret key, add a signature.
			sig := types.TransactionSignature{
				ParentID:       parentID,
				CoveredFields:  cf,
				PublicKeyIndex: uint64(i),
			}
			txn.TransactionSignatures = append(txn.TransactionSignatures, sig)
			sigIndex := len(txn.TransactionSignatures) - 1
			sigHash := txn.SigHash(sigIndex)
			encodedSig, err := crypto.SignHash(sigHash, spendKey.SecretKeys[j])
			if err != nil {
				return err
			}
			txn.TransactionSignatures[sigIndex].Signature = encodedSig[:]

			// Count that the signature has been added, and break out of the
			// secret key loop.
			totalSignatures++
			break
		}

		// If there are enough signatures to satisfy the unlock conditions,
		// break out of the outer loop.
		if totalSignatures == uc.SignaturesRequired {
			break
		}
	}
	return nil
}

// FundSiacoins will add a siacoin input of exactly 'amount' to the
// transaction. A parent transaction may be needed to achieve an input with the
// correct value. The siacoin input will not be signed until 'Sign' is called
// on the transaction builder.
func (tb *transactionBuilder) FundSiacoins(amount types.Currency) error {
	tb.walletSpender.AcquireLock()
	defer tb.walletSpender.ReleaseLock()

	// Collect a value-sorted set of siacoin outputs.
	var so sortedOutputs
	for scoid, sco := range tb.walletSpender.SiacoinOutputs() {
		so.ids = append(so.ids, scoid)
		so.outputs = append(so.outputs, sco)
	}
	// Add all of the unconfirmed outputs as well.
	for _, upt := range tb.walletSpender.UnconfirmedProcessedTransactions() {
		for i, sco := range upt.Transaction.SiacoinOutputs {
			// Determine if the output belongs to the wallet.
			if !tb.walletSpender.HasKey(sco.UnlockHash) {
				continue
			}
			so.ids = append(so.ids, upt.Transaction.SiacoinOutputID(i))
			so.outputs = append(so.outputs, sco)
		}
	}
	sort.Sort(sort.Reverse(so))

	// Create and fund a parent transaction that will add the correct amount of
	// siacoins to the transaction.
	var fund types.Currency
	// potentialFund tracks the balance of the wallet including outputs that
	// have been spent in other unconfirmed transactions recently. This is to
	// provide the user with a more useful error message in the event that they
	// are overspending.
	var potentialFund types.Currency
	parentTxn := types.Transaction{}
	var spentScoids []types.SiacoinOutputID
	for i := range so.ids {
		scoid := so.ids[i]
		sco := so.outputs[i]
		// Check that this output has not recently been spent by the wallet.
		spendHeight := tb.walletSpender.SpendHeight(types.OutputID(scoid))
		// Prevent an underflow error.
		allowedHeight := tb.walletSpender.ConsensusSetHeight() - RespendTimeout
		if tb.walletSpender.ConsensusSetHeight() < RespendTimeout {
			allowedHeight = 0
		}
		if spendHeight > allowedHeight {
			potentialFund = potentialFund.Add(sco.Value)
			continue
		}
		log.Printf("sco=%v", sco)
		log.Printf("Seeking key for %v", sco.UnlockHash)
		outputUnlockConditions := tb.walletSpender.Key(sco.UnlockHash).UnlockConditions
		if tb.walletSpender.ConsensusSetHeight() < outputUnlockConditions.Timelock {
			continue
		}

		// Add a siacoin input for this output.
		sci := types.SiacoinInput{
			ParentID:         scoid,
			UnlockConditions: outputUnlockConditions,
		}
		parentTxn.SiacoinInputs = append(parentTxn.SiacoinInputs, sci)
		spentScoids = append(spentScoids, scoid)

		// Add the output to the total fund
		fund = fund.Add(sco.Value)
		potentialFund = potentialFund.Add(sco.Value)
		if fund.Cmp(amount) >= 0 {
			break
		}
	}
	if potentialFund.Cmp(amount) >= 0 && fund.Cmp(amount) < 0 {
		return modules.ErrPotentialDoubleSpend
	}
	if fund.Cmp(amount) < 0 {
		return modules.ErrLowBalance
	}

	// Create and add the output that will be used to fund the standard
	// transaction.
	parentUnlockConditions, err := tb.walletSpender.NextPrimarySeedAddress()
	if err != nil {
		return err
	}
	exactOutput := types.SiacoinOutput{
		Value:      amount,
		UnlockHash: parentUnlockConditions.UnlockHash(),
	}
	parentTxn.SiacoinOutputs = append(parentTxn.SiacoinOutputs, exactOutput)

	// Create a refund output if needed.
	if amount.Cmp(fund) != 0 {
		refundUnlockConditions, err := tb.walletSpender.NextPrimarySeedAddress()
		if err != nil {
			return err
		}
		refundOutput := types.SiacoinOutput{
			Value:      fund.Sub(amount),
			UnlockHash: refundUnlockConditions.UnlockHash(),
		}
		parentTxn.SiacoinOutputs = append(parentTxn.SiacoinOutputs, refundOutput)
	}
	log.Printf("parentTxn.SiacoinInputs=%v", parentTxn.SiacoinInputs)

	// Sign all of the inputs to the parent transaction.
	for _, sci := range parentTxn.SiacoinInputs {
		log.Printf("seeking key for %v", sci.UnlockConditions.UnlockHash())
		err := addSignatures(&parentTxn, types.FullCoveredFields, sci.UnlockConditions, crypto.Hash(sci.ParentID), tb.walletSpender.Key(sci.UnlockConditions.UnlockHash()))
		if err != nil {
			return err
		}
	}
	// Mark the parent output as spent. Must be done after the transaction is
	// finished because otherwise the txid and output id will change.
	tb.walletSpender.SetSpendHeight(types.OutputID(parentTxn.SiacoinOutputID(0)), tb.walletSpender.ConsensusSetHeight())

	// Add the exact output.
	newInput := types.SiacoinInput{
		ParentID:         parentTxn.SiacoinOutputID(0),
		UnlockConditions: parentUnlockConditions,
	}
	tb.parents = append(tb.parents, parentTxn)
	tb.siacoinInputs = append(tb.siacoinInputs, len(tb.transaction.SiacoinInputs))
	tb.transaction.SiacoinInputs = append(tb.transaction.SiacoinInputs, newInput)

	// Mark all outputs that were spent as spent.
	for _, scoid := range spentScoids {
		tb.walletSpender.SetSpendHeight(types.OutputID(scoid), tb.walletSpender.ConsensusSetHeight())
	}
	return nil
}

// FundSiafunds will add a siafund input of exactly 'amount' to the
// transaction. A parent transaction may be needed to achieve an input with the
// correct value. The siafund input will not be signed until 'Sign' is called
// on the transaction builder.
func (tb *transactionBuilder) FundSiafunds(amount types.Currency) error {
	tb.walletSpender.AcquireLock()
	defer tb.walletSpender.ReleaseLock()

	// Create and fund a parent transaction that will add the correct amount of
	// siafunds to the transaction.
	var fund types.Currency
	var potentialFund types.Currency
	parentTxn := types.Transaction{}
	var spentSfoids []types.SiafundOutputID
	for sfoid, sfo := range tb.walletSpender.SiafundOutputs() {
		// Check that this output has not recently been spent by the wallet.
		spendHeight := tb.walletSpender.SpendHeight(types.OutputID(sfoid))
		// Prevent an underflow error.
		allowedHeight := tb.walletSpender.ConsensusSetHeight() - RespendTimeout
		if tb.walletSpender.ConsensusSetHeight() < RespendTimeout {
			allowedHeight = 0
		}
		if spendHeight > allowedHeight {
			potentialFund = potentialFund.Add(sfo.Value)
			continue
		}
		outputUnlockConditions := tb.walletSpender.Key(sfo.UnlockHash).UnlockConditions
		if tb.walletSpender.ConsensusSetHeight() < outputUnlockConditions.Timelock {
			continue
		}

		// Add a siafund input for this output.
		parentClaimUnlockConditions, err := tb.walletSpender.NextPrimarySeedAddress()
		if err != nil {
			return err
		}
		sfi := types.SiafundInput{
			ParentID:         sfoid,
			UnlockConditions: outputUnlockConditions,
			ClaimUnlockHash:  parentClaimUnlockConditions.UnlockHash(),
		}
		parentTxn.SiafundInputs = append(parentTxn.SiafundInputs, sfi)
		spentSfoids = append(spentSfoids, sfoid)

		// Add the output to the total fund
		fund = fund.Add(sfo.Value)
		potentialFund = potentialFund.Add(sfo.Value)
		if fund.Cmp(amount) >= 0 {
			break
		}
	}
	if potentialFund.Cmp(amount) >= 0 && fund.Cmp(amount) < 0 {
		return modules.ErrPotentialDoubleSpend
	}
	if fund.Cmp(amount) < 0 {
		return modules.ErrLowBalance
	}

	// Create and add the output that will be used to fund the standard
	// transaction.
	parentUnlockConditions, err := tb.walletSpender.NextPrimarySeedAddress()
	if err != nil {
		return err
	}
	exactOutput := types.SiafundOutput{
		Value:      amount,
		UnlockHash: parentUnlockConditions.UnlockHash(),
	}
	parentTxn.SiafundOutputs = append(parentTxn.SiafundOutputs, exactOutput)

	// Create a refund output if needed.
	if amount.Cmp(fund) != 0 {
		refundUnlockConditions, err := tb.walletSpender.NextPrimarySeedAddress()
		if err != nil {
			return err
		}
		refundOutput := types.SiafundOutput{
			Value:      fund.Sub(amount),
			UnlockHash: refundUnlockConditions.UnlockHash(),
		}
		parentTxn.SiafundOutputs = append(parentTxn.SiafundOutputs, refundOutput)
	}

	// Sign all of the inputs to the parent transaction.
	for _, sfi := range parentTxn.SiafundInputs {
		err := addSignatures(&parentTxn, types.FullCoveredFields, sfi.UnlockConditions, crypto.Hash(sfi.ParentID), tb.walletSpender.Key(sfi.UnlockConditions.UnlockHash()))
		if err != nil {
			return err
		}
	}

	// Add the exact output.
	claimUnlockConditions, err := tb.walletSpender.NextPrimarySeedAddress()
	if err != nil {
		return err
	}
	newInput := types.SiafundInput{
		ParentID:         parentTxn.SiafundOutputID(0),
		UnlockConditions: parentUnlockConditions,
		ClaimUnlockHash:  claimUnlockConditions.UnlockHash(),
	}
	tb.parents = append(tb.parents, parentTxn)
	tb.siafundInputs = append(tb.siafundInputs, len(tb.transaction.SiafundInputs))
	tb.transaction.SiafundInputs = append(tb.transaction.SiafundInputs, newInput)

	// Mark all outputs that were spent as spent.
	for _, sfoid := range spentSfoids {
		tb.walletSpender.SetSpendHeight(types.OutputID(sfoid), tb.walletSpender.ConsensusSetHeight())
	}
	return nil
}

// AddMinerFee adds a miner fee to the transaction, returning the index of the
// miner fee within the transaction.
func (tb *transactionBuilder) AddMinerFee(fee types.Currency) uint64 {
	tb.transaction.MinerFees = append(tb.transaction.MinerFees, fee)
	return uint64(len(tb.transaction.MinerFees) - 1)
}

// AddSiacoinInput adds a siacoin input to the transaction, returning the index
// of the siacoin input within the transaction. When 'Sign' gets called, this
// input will be left unsigned.
func (tb *transactionBuilder) AddSiacoinInput(input types.SiacoinInput) uint64 {
	tb.transaction.SiacoinInputs = append(tb.transaction.SiacoinInputs, input)
	return uint64(len(tb.transaction.SiacoinInputs) - 1)
}

// AddSiacoinOutput adds a siacoin output to the transaction, returning the
// index of the siacoin output within the transaction.
func (tb *transactionBuilder) AddSiacoinOutput(output types.SiacoinOutput) uint64 {
	tb.transaction.SiacoinOutputs = append(tb.transaction.SiacoinOutputs, output)
	return uint64(len(tb.transaction.SiacoinOutputs) - 1)
}

// AddFileContract adds a file contract to the transaction, returning the index
// of the file contract within the transaction.
func (tb *transactionBuilder) AddFileContract(fc types.FileContract) uint64 {
	tb.transaction.FileContracts = append(tb.transaction.FileContracts, fc)
	return uint64(len(tb.transaction.FileContracts) - 1)
}

// AddFileContractRevision adds a file contract revision to the transaction,
// returning the index of the file contract revision within the transaction.
// When 'Sign' gets called, this revision will be left unsigned.
func (tb *transactionBuilder) AddFileContractRevision(fcr types.FileContractRevision) uint64 {
	tb.transaction.FileContractRevisions = append(tb.transaction.FileContractRevisions, fcr)
	return uint64(len(tb.transaction.FileContractRevisions) - 1)
}

// AddStorageProof adds a storage proof to the transaction, returning the index
// of the storage proof within the transaction.
func (tb *transactionBuilder) AddStorageProof(sp types.StorageProof) uint64 {
	tb.transaction.StorageProofs = append(tb.transaction.StorageProofs, sp)
	return uint64(len(tb.transaction.StorageProofs) - 1)
}

// AddSiafundInput adds a siafund input to the transaction, returning the index
// of the siafund input within the transaction. When 'Sign' is called, this
// input will be left unsigned.
func (tb *transactionBuilder) AddSiafundInput(input types.SiafundInput) uint64 {
	tb.transaction.SiafundInputs = append(tb.transaction.SiafundInputs, input)
	return uint64(len(tb.transaction.SiafundInputs) - 1)
}

// AddSiafundOutput adds a siafund output to the transaction, returning the
// index of the siafund output within the transaction.
func (tb *transactionBuilder) AddSiafundOutput(output types.SiafundOutput) uint64 {
	tb.transaction.SiafundOutputs = append(tb.transaction.SiafundOutputs, output)
	return uint64(len(tb.transaction.SiafundOutputs) - 1)
}

// AddArbitraryData adds arbitrary data to the transaction, returning the index
// of the data within the transaction.
func (tb *transactionBuilder) AddArbitraryData(arb []byte) uint64 {
	tb.transaction.ArbitraryData = append(tb.transaction.ArbitraryData, arb)
	return uint64(len(tb.transaction.ArbitraryData) - 1)
}

// AddTransactionSignature adds a transaction signature to the transaction,
// returning the index of the signature within the transaction. The signature
// should already be valid, and shouldn't sign any of the inputs that were
// added by calling 'FundSiacoins' or 'FundSiafunds'.
func (tb *transactionBuilder) AddTransactionSignature(sig types.TransactionSignature) uint64 {
	tb.transaction.TransactionSignatures = append(tb.transaction.TransactionSignatures, sig)
	return uint64(len(tb.transaction.TransactionSignatures) - 1)
}

// Drop discards all of the outputs in a transaction, returning them to the
// pool so that other transactions may use them. 'Drop' should only be called
// if a transaction is both unsigned and will not be used any further.
func (tb *transactionBuilder) Drop() {
	tb.walletSpender.AcquireLock()
	defer tb.walletSpender.ReleaseLock()

	// Iterate through all parents and the transaction itself and restore all
	// outputs to the list of available outputs.
	txns := append(tb.parents, tb.transaction)
	for _, txn := range txns {
		for _, sci := range txn.SiacoinInputs {
			tb.walletSpender.RemoveOutput(types.OutputID(sci.ParentID))
		}
	}

	tb.parents = nil
	tb.transaction = types.Transaction{}
	tb.siacoinInputs = nil
	tb.siafundInputs = nil
}

// Sign will sign any inputs added by 'FundSiacoins' or 'FundSiafunds' and
// return a transaction set that contains all parents prepended to the
// transaction. If more fields need to be added, a new transaction builder will
// need to be created.
//
// If the whole transaction flag  is set to true, then the whole transaction
// flag will be set in the covered fields object. If the whole transaction flag
// is set to false, then the covered fields object will cover all fields that
// have already been added to the transaction, but will also leave room for
// more fields to be added.
func (tb *transactionBuilder) Sign(wholeTransaction bool) ([]types.Transaction, error) {
	// Create the coveredfields struct.
	txn := tb.transaction
	var coveredFields types.CoveredFields
	if wholeTransaction {
		coveredFields = types.CoveredFields{WholeTransaction: true}
	} else {
		for i := range txn.MinerFees {
			coveredFields.MinerFees = append(coveredFields.MinerFees, uint64(i))
		}
		for i := range txn.SiacoinInputs {
			coveredFields.SiacoinInputs = append(coveredFields.SiacoinInputs, uint64(i))
		}
		for i := range txn.SiacoinOutputs {
			coveredFields.SiacoinOutputs = append(coveredFields.SiacoinOutputs, uint64(i))
		}
		for i := range txn.FileContracts {
			coveredFields.FileContracts = append(coveredFields.FileContracts, uint64(i))
		}
		for i := range txn.FileContractRevisions {
			coveredFields.FileContractRevisions = append(coveredFields.FileContractRevisions, uint64(i))
		}
		for i := range txn.StorageProofs {
			coveredFields.StorageProofs = append(coveredFields.StorageProofs, uint64(i))
		}
		for i := range txn.SiafundInputs {
			coveredFields.SiafundInputs = append(coveredFields.SiafundInputs, uint64(i))
		}
		for i := range txn.SiafundOutputs {
			coveredFields.SiafundOutputs = append(coveredFields.SiafundOutputs, uint64(i))
		}
		for i := range txn.ArbitraryData {
			coveredFields.ArbitraryData = append(coveredFields.ArbitraryData, uint64(i))
		}
	}
	// TransactionSignatures don't get covered by the 'WholeTransaction' flag,
	// and must be covered manually.
	for i := range txn.TransactionSignatures {
		coveredFields.TransactionSignatures = append(coveredFields.TransactionSignatures, uint64(i))
	}

	// For each siacoin input in the transaction that we added, provide a
	// signature.
	tb.walletSpender.AcquireLock()
	defer tb.walletSpender.ReleaseLock()
	for _, inputIndex := range tb.siacoinInputs {
		input := txn.SiacoinInputs[inputIndex]
		key := tb.walletSpender.Key(input.UnlockConditions.UnlockHash())
		err := addSignatures(&txn, coveredFields, input.UnlockConditions, crypto.Hash(input.ParentID), key)
		if err != nil {
			return nil, err
		}
	}
	for _, inputIndex := range tb.siafundInputs {
		input := txn.SiafundInputs[inputIndex]
		key := tb.walletSpender.Key(input.UnlockConditions.UnlockHash())
		err := addSignatures(&txn, coveredFields, input.UnlockConditions, crypto.Hash(input.ParentID), key)
		if err != nil {
			return nil, err
		}
	}

	// Get the transaction set and delete the transaction from the registry.
	txnSet := append(tb.parents, txn)
	return txnSet, nil
}

// ViewTransaction returns a transaction-in-progress along with all of its
// parents, specified by id. An error is returned if the id is invalid.  Note
// that ids become invalid for a transaction after 'SignTransaction' has been
// called because the transaction gets deleted.
func (tb *transactionBuilder) View() (types.Transaction, []types.Transaction) {
	return tb.transaction, tb.parents
}

// RegisterTransaction takes a transaction and its parents and returns a
// TransactionBuilder which can be used to expand the transaction. The most
// typical call is 'RegisterTransaction(types.Transaction{}, nil)', which
// registers a new transaction without parents.
func (w *Wallet) RegisterTransaction(t types.Transaction, parents []types.Transaction) modules.TransactionBuilder {
	return &transactionBuilder{
		parents:     parents,
		transaction: t,

		walletSpender: &spendableWallet{wallet: w},
	}
}

// StartTransaction is a convenience function that calls
// RegisterTransaction(types.Transaction{}, nil).
func (w *Wallet) StartTransaction() modules.TransactionBuilder {
	return w.RegisterTransaction(types.Transaction{}, nil)
}
