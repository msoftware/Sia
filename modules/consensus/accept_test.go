package consensus

import (
	"testing"
	"time"

	"github.com/NebulousLabs/Sia/crypto"
	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/types"
)

// TestIntegrationDoSBlockHandling checks that saved bad blocks are correctly ignored.
func TestIntegrationDoSBlockHandling(t *testing.T) {
	// TestIntegrationDoSBlockHandling catches a wide array of simple errors,
	// and therefore is included in the short tests despite being somewhat
	// computationally expensive.
	cst, err := createConsensusSetTester("TestIntegrationDoSBlockHandling")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Mine a block that is valid except for containing a buried invalid
	// transaction. The transaction has more siacoin inputs than outputs.
	txnBuilder := cst.wallet.StartTransaction()
	err = txnBuilder.FundSiacoins(types.NewCurrency64(50))
	if err != nil {
		t.Fatal(err)
	}
	txnSet, err := txnBuilder.Sign(true) // true sets the 'wholeTransaction' flag
	if err != nil {
		t.Fatal(err)
	}

	// Mine and submit the invalid block to the consensus set. The first time
	// around, the complaint should be about the rule-breaking transaction.
	block, _, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.Transactions = append(block.Transactions, txnSet...)
	dosBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(dosBlock)
	if err != errSiacoinInputOutputMismatch {
		t.Fatalf("expected %v, got %v", errSiacoinInputOutputMismatch, err)
	}

	// Submit the same block a second time. The complaint should be that the
	// block is already known to be invalid.
	err = cst.cs.AcceptBlock(dosBlock)
	if err != errDoSBlock {
		t.Fatalf("expected %v, got %v", errDoSBlock, err)
	}
}

// TestBlockKnownHandling submits known blocks to the consensus set.
func TestBlockKnownHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestBlockKnownHandling")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Get a block destined to be stale.
	block, _, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	staleBlock, _ := cst.miner.SolveBlock(block, target)

	// Add two new blocks to the consensus set to block the stale block.
	block1, err := cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}
	block2, err := cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Submit the stale block.
	err = cst.cs.AcceptBlock(staleBlock)
	if err != nil && err != modules.ErrNonExtendingBlock {
		t.Fatal(err)
	}

	// Submit all the blocks again, looking for a 'stale block' error.
	err = cst.cs.AcceptBlock(block1)
	if err != modules.ErrBlockKnown {
		t.Fatalf("expected %v, got %v", modules.ErrBlockKnown, err)
	}
	err = cst.cs.AcceptBlock(block2)
	if err != modules.ErrBlockKnown {
		t.Fatalf("expected %v, got %v", modules.ErrBlockKnown, err)
	}
	err = cst.cs.AcceptBlock(staleBlock)
	if err != modules.ErrBlockKnown {
		t.Fatalf("expected %v, got %v", modules.ErrBlockKnown, err)
	}

	// Try submitting the genesis block.
	id, err := cst.cs.dbGetPath(0)
	if err != nil {
		t.Fatal(err)
	}
	genesisBlock, err := cst.cs.dbGetBlockMap(id)
	if err != nil {
		t.Fatal(err)
	}
	err = cst.cs.AcceptBlock(genesisBlock.Block)
	if err != modules.ErrBlockKnown {
		t.Fatalf("expected %v, got %v", modules.ErrBlockKnown, err)
	}
}

// TestOrphanHandling passes an orphan block to the consensus set.
func TestOrphanHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestOrphanHandling")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Try submitting an orphan block to the consensus set. The empty block can
	// be used, because looking for a parent is one of the first checks the
	// consensus set performs.
	orphan := types.Block{}
	err = cst.cs.AcceptBlock(orphan)
	if err != errOrphan {
		t.Fatalf("expected %v, got %v", errOrphan, err)
	}
	err = cst.cs.AcceptBlock(orphan)
	if err != errOrphan {
		t.Fatalf("expected %v, got %v", errOrphan, err)
	}
}

// TestMissedTarget submits a block that does not meet the required target.
func TestMissedTarget(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestMissedTarget")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Mine a block that doesn't meet the target.
	block, _, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	for block.CheckTarget(target) && block.Nonce[0] != 255 {
		block.Nonce[0]++
	}
	if block.CheckTarget(target) {
		t.Fatal("unable to find a failing target")
	}
	err = cst.cs.AcceptBlock(block)
	if err != errMissedTarget {
		t.Fatalf("expected %v, got %v", errMissedTarget, err)
	}
}

// testLargeBlock checks that large blocks are rejected.
func TestLargeBlock(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestLargeBlock")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Create a transaction that puts the block over the size limit.
	bigData := make([]byte, types.BlockSizeLimit)
	txn := types.Transaction{
		ArbitraryData: [][]byte{bigData},
	}

	// Fetch a block and add the transaction, then submit the block.
	block, _, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.Transactions = append(block.Transactions, txn)
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(solvedBlock)
	if err != errLargeBlock {
		t.Fatalf("expected %v, got %v", errLargeBlock, err)
	}
}

// TestEarlyBlockTimestampHandling checks that blocks with early timestamps are
// handled appropriately.
func TestEarlyBlockTimestampHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestBlockTimestampHandling")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Create a block with a too early timestamp - block should be rejected
	// outright.
	block, _, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.Timestamp = 0
	earlyBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(earlyBlock)
	if err != errEarlyTimestamp {
		t.Fatalf("expected %v, got %v", errEarlyTimestamp, err)
	}
}

// TestExtremeFutureTimestampHandling checks that blocks with extreme future
// timestamps handled correclty.
func TestExtremeFutureTimestampHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestExtremeFutureTimestampHandling")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Submit a block with a timestamp in the extreme future.
	block, _, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.Timestamp = types.CurrentTimestamp() + 2 + types.ExtremeFutureThreshold
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(solvedBlock)
	if err != errExtremeFutureTimestamp {
		t.Fatalf("expected %v, got %v", errExtremeFutureTimestamp, err)
	}

	// Check that after waiting until the block is no longer in the future, the
	// block still has not been added to the consensus set (prove that the
	// block was correctly discarded).
	time.Sleep(time.Second * time.Duration(3+types.ExtremeFutureThreshold))
	_, err = cst.cs.dbGetBlockMap(solvedBlock.ID())
	if err != errNilItem {
		t.Error("extreme future block made it into the consensus set after waiting")
	}
}

// TestMinerPayoutHandling checks that blocks with incorrect payouts are
// rejected.
func TestMinerPayoutHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestMinerPayoutHandling")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Create a block with the wrong miner payout structure - testing can be
	// light here because there is heavier testing in the 'types' package,
	// where the logic is defined.
	block, _, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.MinerPayouts = append(block.MinerPayouts, types.SiacoinOutput{Value: types.NewCurrency64(1)})
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(solvedBlock)
	if err != errBadMinerPayouts {
		t.Fatalf("expected %v, got %v", errBadMinerPayouts, err)
	}
}

// testFutureTimestampHandling checks that blocks in the future (but not
// extreme future) are handled correctly.
func TestFutureTimestampHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestFutureTimestampHandling")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Submit a block with a timestamp in the future, but not the extreme
	// future.
	block, _, target, err := cst.miner.BlockForWork()
	if err != nil {
		t.Fatal(err)
	}
	block.Timestamp = types.CurrentTimestamp() + 2 + types.FutureThreshold
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(solvedBlock)
	if err != errFutureTimestamp {
		t.Fatalf("expected %v, got %v", errFutureTimestamp, err)
	}

	// Check that after waiting until the block is no longer too far in the
	// future, the block gets added to the consensus set.
	time.Sleep(time.Second * 3) // 3 seconds, as the block was originally 2 seconds too far into the future.
	_, err = cst.cs.dbGetBlockMap(solvedBlock.ID())
	if err == errNilItem {
		t.Fatalf("future block was not added to the consensus set after waiting the appropriate amount of time")
	}
}

// TestBuriedBadTransaction tries submitting a block with a bad transaction
// that is buried under good transactions.
func TestBuriedBadTransaction(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestBuriedBadTransaction")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()
	pb := cst.cs.dbCurrentProcessedBlock()

	// Create a good transaction using the wallet.
	txnValue := types.NewCurrency64(1200)
	txnBuilder := cst.wallet.StartTransaction()
	err = txnBuilder.FundSiacoins(txnValue)
	if err != nil {
		t.Fatal(err)
	}
	txnBuilder.AddSiacoinOutput(types.SiacoinOutput{Value: txnValue})
	txnSet, err := txnBuilder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}
	err = cst.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}

	// Create a bad transaction
	badTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{}},
	}
	txns := append(cst.tpool.TransactionList(), badTxn)

	// Create a block with a buried bad transaction.
	block := types.Block{
		ParentID:     pb.Block.ID(),
		Timestamp:    types.CurrentTimestamp(),
		MinerPayouts: []types.SiacoinOutput{{Value: types.CalculateCoinbase(pb.Height + 1)}},
		Transactions: txns,
	}
	block, _ = cst.miner.SolveBlock(block, pb.ChildTarget)
	err = cst.cs.AcceptBlock(block)
	if err == nil {
		t.Error("buried transaction didn't cause an error")
	}
}

// TestInconsistencyCheck puts the consensus set in to an inconsistent state
// and makes sure that the santiy checks are triggering panics.
func TestInconsistentCheck(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestInconsistentCheck")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Corrupt the consensus set by adding a new siafund output.
	sfo := types.SiafundOutput{
		Value: types.NewCurrency64(1),
	}
	cst.cs.dbAddSiafundOutput(types.SiafundOutputID{}, sfo)

	// Catch a panic that should be caused by the inconsistency check after a
	// block is mined.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("inconsistency panic not triggered by corrupted database")
		}
	}()
	cst.miner.AddBlock()
}

// COMPATv0.4.0
//
// This test checks that the hardfork scheduled for block 21,000 rolls through
// smoothly.
func TestTaxHardfork(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestTaxHardfork")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Create a file contract with a payout that is put into the blockchain
	// before the hardfork block but expires after the hardfork block.
	payout := types.NewCurrency64(400e6)
	outputSize := types.PostTax(cst.cs.dbBlockHeight(), payout)
	fc := types.FileContract{
		WindowStart:        cst.cs.dbBlockHeight() + 12,
		WindowEnd:          cst.cs.dbBlockHeight() + 14,
		Payout:             payout,
		ValidProofOutputs:  []types.SiacoinOutput{{Value: outputSize}},
		MissedProofOutputs: []types.SiacoinOutput{{Value: outputSize}},
		UnlockHash:         types.UnlockConditions{}.UnlockHash(), // The empty UC is anyone-can-spend
	}

	// Create and fund a transaction with a file contract.
	txnBuilder := cst.wallet.StartTransaction()
	err = txnBuilder.FundSiacoins(payout)
	if err != nil {
		t.Fatal(err)
	}
	fcIndex := txnBuilder.AddFileContract(fc)
	txnSet, err := txnBuilder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}
	err = cst.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Check that the siafund pool was increased by the faulty float amount.
	siafundPool := cst.cs.dbGetSiafundPool()
	if siafundPool.Cmp(types.NewCurrency64(15590e3)) != 0 {
		t.Fatal("siafund pool was not increased correctly")
	}

	// Mine blocks until the hardfork is reached.
	for i := 0; i < 10; i++ {
		_, err = cst.miner.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Submit a file contract revision and check that the payouts are able to
	// be the same.
	fcid := txnSet[len(txnSet)-1].FileContractID(fcIndex)
	fcr := types.FileContractRevision{
		ParentID:          fcid,
		UnlockConditions:  types.UnlockConditions{},
		NewRevisionNumber: 1,

		NewFileSize:           1,
		NewWindowStart:        cst.cs.dbBlockHeight() + 2,
		NewWindowEnd:          cst.cs.dbBlockHeight() + 4,
		NewValidProofOutputs:  fc.ValidProofOutputs,
		NewMissedProofOutputs: fc.MissedProofOutputs,
	}
	txnBuilder = cst.wallet.StartTransaction()
	txnBuilder.AddFileContractRevision(fcr)
	txnSet, err = txnBuilder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}
	err = cst.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Mine blocks until the revision goes through, such that the sanity checks
	// can be run.
	for i := 0; i < 6; i++ {
		_, err = cst.miner.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Create a 2-of-2 unlock conditions, 1 key for each the sender and the
	// receiver in the payment channel.
	skg := crypto.NewSignatureKeyGenerator()
	sk1, pk1, err := skg.Generate() // Funding entity.
	if err != nil {
		t.Fatal(err)
	}

	sk2, pk2, err := skg.Generate() // Receiving entity.
	if err != nil {
		t.Fatal(err)
	}
	uc := types.UnlockConditions{
		PublicKeys: []types.SiaPublicKey{
			{
				Algorithm: types.SignatureEd25519,
				Key:       pk1[:],
			},
			{
				Algorithm: types.SignatureEd25519,
				Key:       pk2[:],
			},
		},
		SignaturesRequired: 2,
	}
	channelAddress := uc.UnlockHash()

	// Funding entity creates but does not sign a transaction that funds the
	// channel address. Because the wallet is not very flexible, the channel
	// txn needs to be fully custom. To get a custom txn, manually create an
	// address and then use the wallet to fund that address.
	channelSize := types.NewCurrency64(10e3)
	channelFundingSK, channelFundingPK, err := skg.Generate()
	if err != nil {
		t.Fatal(err)
		return err
	}
	channelFundingUC := types.UnlockConditions{
		PublicKeys: []types.SiaPublicKey{{
			Algorithm: types.SignatureEd25519,
			Key:       channelFundingPK[:],
		}},
		SignaturesRequired: 1,
	}
	channelFundingAddr := channelFundingUC.UnlockHash()
	fundTxnBuilder := cst.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	err = fundTxnBuilder.FundSiacoins(channelSize)
	if err != nil {
		t.Fatal(err)
	}
	scoFundIndex := fundTxnBuilder.AddSiacoinOutput(types.SiacoinOutput{Value: channelSize, UnlockHash: channelFundingAddr})
	fundTxnSet, err := fundTxnBuilder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}
	fundOutputID := fundTxnSet[len(fundTxnSet)-1].SiacoinOutputID(int(scoFundIndex))
	channelTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         fundOutputID,
			UnlockConditions: channelFundingUC,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      channelSize,
			UnlockHash: channelAddress,
		}},
		TransactionSignatures: []types.TransactionSignature{{
			ParentID:       crypto.Hash(fundOutputID),
			PublicKeyIndex: 0,
			CoveredFields:  types.CoveredFields{WholeTransaction: true},
		}},
	}

	// Funding entity creates and signs a transaction that spends the full
	// channel output.
	channelOutputID := channelTxn.SiacoinOutputID(0)
	refundUC, err := cst.wallet.NextAddress()
	refundAddr := refundUC.UnlockHash()
	if err != nil {
		return err
	}
	refundTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         channelOutputID,
			UnlockConditions: uc,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      channelSize,
			UnlockHash: refundAddr,
		}},
		TransactionSignatures: []types.TransactionSignature{{
			ParentID:       crypto.Hash(channelOutputID),
			PublicKeyIndex: 0,
			CoveredFields:  types.CoveredFields{WholeTransaction: true},
		}},
	}
	sigHash := refundTxn.SigHash(0)
	cryptoSig1, err := crypto.SignHash(sigHash, sk1)
	if err != nil {
		return err
	}
	refundTxn.TransactionSignatures[0].Signature = cryptoSig1[:]

	// Receiving entity signs the transaction that spends the full channel
	// output, but with a timelock.
	refundTxn.TransactionSignatures = append(refundTxn.TransactionSignatures, types.TransactionSignature{
		ParentID:       crypto.Hash(channelOutputID),
		PublicKeyIndex: 1,
		Timelock:       cst.cs.height() + 2,
		CoveredFields:  types.CoveredFields{WholeTransaction: true},
	})
	sigHash = refundTxn.SigHash(1)
	cryptoSig2, err := crypto.SignHash(sigHash, sk2)
	if err != nil {
		return err
	}
	refundTxn.TransactionSignatures[1].Signature = cryptoSig2[:]

	// Funding entity will now sign and broadcast the funding transaction.
	sigHash = channelTxn.SigHash(0)
	cryptoSig0, err := crypto.SignHash(sigHash, channelFundingSK)
	if err != nil {
		return err
	}
	channelTxn.TransactionSignatures[0].Signature = cryptoSig0[:]
	err = cst.tpool.AcceptTransactionSet(append(fundTxnSet, channelTxn))
	if err != nil {
		return err
	}
	// Put the txn in a block.
	_, err = cst.miner.AddBlock()
	if err != nil {
		return err
	}

	// Try to submit the refund transaction before the timelock has expired.
	err = cst.tpool.AcceptTransactionSet([]types.Transaction{refundTxn})
	if err != types.ErrPrematureSignature {
		return err
	}

	// Create a transaction that has partially used the channel, and submit it
	// to the blockchain to close the channel.
	closeTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         channelOutputID,
			UnlockConditions: uc,
		}},
		SiacoinOutputs: []types.SiacoinOutput{
			{
				Value:      channelSize.Sub(types.NewCurrency64(5)),
				UnlockHash: refundAddr,
			},
			{
				Value: types.NewCurrency64(5),
			},
		},
		TransactionSignatures: []types.TransactionSignature{
			{
				ParentID:       crypto.Hash(channelOutputID),
				PublicKeyIndex: 0,
				CoveredFields:  types.CoveredFields{WholeTransaction: true},
			},
			{
				ParentID:       crypto.Hash(channelOutputID),
				PublicKeyIndex: 1,
				CoveredFields:  types.CoveredFields{WholeTransaction: true},
			},
		},
	}
	sigHash = closeTxn.SigHash(0)
	cryptoSig3, err := crypto.SignHash(sigHash, sk1)
	if err != nil {
		return err
	}
	closeTxn.TransactionSignatures[0].Signature = cryptoSig3[:]
	sigHash = closeTxn.SigHash(1)
	cryptoSig4, err := crypto.SignHash(sigHash, sk2)
	if err != nil {
		return err
	}
	closeTxn.TransactionSignatures[1].Signature = cryptoSig4[:]
	err = cst.tpool.AcceptTransactionSet([]types.Transaction{closeTxn})
	if err != nil {
		return err
	}

	// Mine the block with the transaction.
	_, err = cst.miner.AddBlock()
	if err != nil {
		return err
	}
	closeRefundID := closeTxn.SiacoinOutputID(0)
	closePaymentID := closeTxn.SiacoinOutputID(1)
	exists := cst.cs.db.inSiacoinOutputs(closeRefundID)
	if !exists {
		return errors.New("close txn refund output doesn't exist")
	}
	exists = cst.cs.db.inSiacoinOutputs(closePaymentID)
	if !exists {
		return errors.New("close txn payment output doesn't exist")
	}

	// Create a payment channel where the receiving entity never responds to
	// the initial transaction.
	{
		// Funding entity creates but does not sign a transaction that funds the
		// channel address. Because the wallet is not very flexible, the channel
		// txn needs to be fully custom. To get a custom txn, manually create an
		// address and then use the wallet to fund that address.
		channelSize := types.NewCurrency64(10e3)
		channelFundingSK, channelFundingPK, err := skg.Generate()
		if err != nil {
			return err
		}
		channelFundingUC := types.UnlockConditions{
			PublicKeys: []types.SiaPublicKey{{
				Algorithm: types.SignatureEd25519,
				Key:       channelFundingPK[:],
			}},
			SignaturesRequired: 1,
		}
		channelFundingAddr := channelFundingUC.UnlockHash()
		fundTxnBuilder := cst.wallet.StartTransaction()
		err = fundTxnBuilder.FundSiacoins(channelSize)
		if err != nil {
			return err
		}
		scoFundIndex := fundTxnBuilder.AddSiacoinOutput(types.SiacoinOutput{Value: channelSize, UnlockHash: channelFundingAddr})
		fundTxnSet, err := fundTxnBuilder.Sign(true)
		if err != nil {
			return err
		}
		fundOutputID := fundTxnSet[len(fundTxnSet)-1].SiacoinOutputID(int(scoFundIndex))
		channelTxn := types.Transaction{
			SiacoinInputs: []types.SiacoinInput{{
				ParentID:         fundOutputID,
				UnlockConditions: channelFundingUC,
			}},
			SiacoinOutputs: []types.SiacoinOutput{{
				Value:      channelSize,
				UnlockHash: channelAddress,
			}},
			TransactionSignatures: []types.TransactionSignature{{
				ParentID:       crypto.Hash(fundOutputID),
				PublicKeyIndex: 0,
				CoveredFields:  types.CoveredFields{WholeTransaction: true},
			}},
		}

		// Funding entity creates and signs a transaction that spends the full
		// channel output.
		channelOutputID := channelTxn.SiacoinOutputID(0)
		refundUC, err := cst.wallet.NextAddress()
		refundAddr := refundUC.UnlockHash()
		if err != nil {
			return err
		}
		refundTxn := types.Transaction{
			SiacoinInputs: []types.SiacoinInput{{
				ParentID:         channelOutputID,
				UnlockConditions: uc,
			}},
			SiacoinOutputs: []types.SiacoinOutput{{
				Value:      channelSize,
				UnlockHash: refundAddr,
			}},
			TransactionSignatures: []types.TransactionSignature{{
				ParentID:       crypto.Hash(channelOutputID),
				PublicKeyIndex: 0,
				CoveredFields:  types.CoveredFields{WholeTransaction: true},
			}},
		}
		sigHash := refundTxn.SigHash(0)
		cryptoSig1, err := crypto.SignHash(sigHash, sk1)
		if err != nil {
			return err
		}
		refundTxn.TransactionSignatures[0].Signature = cryptoSig1[:]

		// Recieving entity never communitcates, funding entity must reclaim
		// the 'channelSize' coins that were intended to go to the channel.
		reclaimUC, err := cst.wallet.NextAddress()
		reclaimAddr := reclaimUC.UnlockHash()
		if err != nil {
			return err
		}
		reclaimTxn := types.Transaction{
			SiacoinInputs: []types.SiacoinInput{{
				ParentID:         fundOutputID,
				UnlockConditions: channelFundingUC,
			}},
			SiacoinOutputs: []types.SiacoinOutput{{
				Value:      channelSize,
				UnlockHash: reclaimAddr,
			}},
			TransactionSignatures: []types.TransactionSignature{{
				ParentID:       crypto.Hash(fundOutputID),
				PublicKeyIndex: 0,
				CoveredFields:  types.CoveredFields{WholeTransaction: true},
			}},
		}
		sigHash = reclaimTxn.SigHash(0)
		cryptoSig, err := crypto.SignHash(sigHash, channelFundingSK)
		if err != nil {
			return err
		}
		reclaimTxn.TransactionSignatures[0].Signature = cryptoSig[:]
		err = cst.tpool.AcceptTransactionSet(append(fundTxnSet, reclaimTxn))
		if err != nil {
			return err
		}
		block, _ := cst.miner.FindBlock()
		err = cst.cs.AcceptBlock(block)
		if err != nil {
			return err
		}
		reclaimOutputID := reclaimTxn.SiacoinOutputID(0)
		exists := cst.cs.db.inSiacoinOutputs(reclaimOutputID)
		if !exists {
			return errors.New("failed to reclaim an output that belongs to the funding entity")
		}
	}

	// Create a channel and the open the channel, but close the channel using
	// the timelocked signature.
	{
		// Funding entity creates but does not sign a transaction that funds the
		// channel address. Because the wallet is not very flexible, the channel
		// txn needs to be fully custom. To get a custom txn, manually create an
		// address and then use the wallet to fund that address.
		channelSize := types.NewCurrency64(10e3)
		channelFundingSK, channelFundingPK, err := skg.Generate()
		if err != nil {
			return err
		}
		channelFundingUC := types.UnlockConditions{
			PublicKeys: []types.SiaPublicKey{{
				Algorithm: types.SignatureEd25519,
				Key:       channelFundingPK[:],
			}},
			SignaturesRequired: 1,
		}
		channelFundingAddr := channelFundingUC.UnlockHash()
		fundTxnBuilder := cst.wallet.StartTransaction()
		err = fundTxnBuilder.FundSiacoins(channelSize)
		if err != nil {
			return err
		}
		scoFundIndex := fundTxnBuilder.AddSiacoinOutput(types.SiacoinOutput{Value: channelSize, UnlockHash: channelFundingAddr})
		fundTxnSet, err := fundTxnBuilder.Sign(true)
		if err != nil {
			return err
		}
		fundOutputID := fundTxnSet[len(fundTxnSet)-1].SiacoinOutputID(int(scoFundIndex))
		channelTxn := types.Transaction{
			SiacoinInputs: []types.SiacoinInput{{
				ParentID:         fundOutputID,
				UnlockConditions: channelFundingUC,
			}},
			SiacoinOutputs: []types.SiacoinOutput{{
				Value:      channelSize,
				UnlockHash: channelAddress,
			}},
			TransactionSignatures: []types.TransactionSignature{{
				ParentID:       crypto.Hash(fundOutputID),
				PublicKeyIndex: 0,
				CoveredFields:  types.CoveredFields{WholeTransaction: true},
			}},
		}

		// Funding entity creates and signs a transaction that spends the full
		// channel output.
		channelOutputID := channelTxn.SiacoinOutputID(0)
		refundUC, err := cst.wallet.NextAddress()
		refundAddr := refundUC.UnlockHash()
		if err != nil {
			return err
		}
		refundTxn := types.Transaction{
			SiacoinInputs: []types.SiacoinInput{{
				ParentID:         channelOutputID,
				UnlockConditions: uc,
			}},
			SiacoinOutputs: []types.SiacoinOutput{{
				Value:      channelSize,
				UnlockHash: refundAddr,
			}},
			TransactionSignatures: []types.TransactionSignature{{
				ParentID:       crypto.Hash(channelOutputID),
				PublicKeyIndex: 0,
				CoveredFields:  types.CoveredFields{WholeTransaction: true},
			}},
		}
		sigHash := refundTxn.SigHash(0)
		cryptoSig1, err := crypto.SignHash(sigHash, sk1)
		if err != nil {
			return err
		}
		refundTxn.TransactionSignatures[0].Signature = cryptoSig1[:]

		// Receiving entity signs the transaction that spends the full channel
		// output, but with a timelock.
		refundTxn.TransactionSignatures = append(refundTxn.TransactionSignatures, types.TransactionSignature{
			ParentID:       crypto.Hash(channelOutputID),
			PublicKeyIndex: 1,
			Timelock:       cst.cs.height() + 2,
			CoveredFields:  types.CoveredFields{WholeTransaction: true},
		})
		sigHash = refundTxn.SigHash(1)
		cryptoSig2, err := crypto.SignHash(sigHash, sk2)
		if err != nil {
			return err
		}
		refundTxn.TransactionSignatures[1].Signature = cryptoSig2[:]

		// Funding entity will now sign and broadcast the funding transaction.
		sigHash = channelTxn.SigHash(0)
		cryptoSig0, err := crypto.SignHash(sigHash, channelFundingSK)
		if err != nil {
			return err
		}
		channelTxn.TransactionSignatures[0].Signature = cryptoSig0[:]
		err = cst.tpool.AcceptTransactionSet(append(fundTxnSet, channelTxn))
		if err != nil {
			return err
		}
		// Put the txn in a block.
		block, _ := cst.miner.FindBlock()
		err = cst.cs.AcceptBlock(block)
		if err != nil {
			return err
		}

		// Receiving entity never signs another transaction, so the funding
		// entity waits until the timelock is complete, and then submits the
		// refundTxn.
		for i := 0; i < 3; i++ {
			block, _ := cst.miner.FindBlock()
			err = cst.cs.AcceptBlock(block)
			if err != nil {
				return err
			}
		}
		err = cst.tpool.AcceptTransactionSet([]types.Transaction{refundTxn})
		if err != nil {
			return err
		}
		block, _ = cst.miner.FindBlock()
		err = cst.cs.AcceptBlock(block)
		if err != nil {
			return err
		}
		refundOutputID := refundTxn.SiacoinOutputID(0)
		exists := cst.cs.db.inSiacoinOutputs(refundOutputID)
		if !exists {
			return errors.New("timelocked refund transaction did not get spent correctly")
		}
	}

	return nil
}

// TestPaymentChannelBlocks creates a consensus set tester and uses it to call
// testPaymentChannelBlocks.
func TestPaymentChannelBlocks(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestPaymentChannelBlocks")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()
	err = cst.testPaymentChannelBlocks()
	if err != nil {
		t.Fatal(err)
	}
}

// complexBlockSet puts a set of blocks with many types of transactions into
// the consensus set.
func (cst *consensusSetTester) complexBlockSet() error {
	err := cst.testSimpleBlock()
	if err != nil {
		return err
	}
	err = cst.testSpendSiacoinsBlock()
	if err != nil {
		return err
	}

	// COMPATv0.4.0
	//
	// Mine enough blocks to get above the file contract hardfork threshold
	// (10).
	for i := 0; i < 10; i++ {
		block, _ := cst.miner.FindBlock()
		err = cst.cs.AcceptBlock(block)
		if err != nil {
			return err
		}
	}

	err = cst.testFileContractsBlocks()
	if err != nil {
		return err
	}
	err = cst.testSpendSiafundsBlock()
	if err != nil {
		return err
	}
	return nil
}

// TestComplexForking adds every type of test block into two parallel chains of
// consensus, and then forks to a new chain, forcing the whole structure to be
// reverted.
func TestComplexForking(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst1, err := createConsensusSetTester("TestComplexForking - 1")
	if err != nil {
		t.Fatal(err)
	}
	defer cst1.closeCst()
	cst2, err := createConsensusSetTester("TestComplexForking - 2")
	if err != nil {
		t.Fatal(err)
	}
	defer cst2.closeCst()
	cst3, err := createConsensusSetTester("TestComplexForking - 3")
	if err != nil {
		t.Fatal(err)
	}
	defer cst3.closeCst()

	// Give each type of major block to cst1.
	err = cst1.complexBlockSet()
	if err != nil {
		t.Fatal(err)
	}

	// Give all the blocks in cst1 to cst3 - as a holding place.
	var cst1Blocks []types.Block
	pb := cst1.cs.currentProcessedBlock()
	for pb.Block.ID() != cst1.cs.blockRoot.Block.ID() {
		cst1Blocks = append([]types.Block{pb.Block}, cst1Blocks...) // prepend
		pb = cst1.cs.db.getBlockMap(pb.Parent)
	}

	for _, block := range cst1Blocks {
		// Some blocks will return errors.
		err = cst3.cs.AcceptBlock(block)
		if err == nil {
		}
	}
	if cst3.cs.currentBlockID() != cst1.cs.currentBlockID() {
		t.Error("cst1 and cst3 do not share the same path")
	}
	if cst3.cs.consensusSetHash() != cst1.cs.consensusSetHash() {
		t.Error("cst1 and cst3 do not share a consensus set hash")
	}

	// Mine 3 blocks on cst2, then all the block types, to give it a heavier
	// weight, then give all of its blocks to cst1. This will cause a complex
	// fork to happen.
	for i := 0; i < 3; i++ {
		block, _ := cst2.miner.FindBlock()
		err = cst2.cs.AcceptBlock(block)
		if err != nil {
			t.Fatal(err)
		}
	}
	err = cst2.complexBlockSet()
	if err != nil {
		t.Fatal(err)
	}
	var cst2Blocks []types.Block
	pb = cst2.cs.currentProcessedBlock()
	for pb.Block.ID() != cst2.cs.blockRoot.Block.ID() {
		cst2Blocks = append([]types.Block{pb.Block}, cst2Blocks...) // prepend
		pb = cst2.cs.db.getBlockMap(pb.Parent)
	}
	for _, block := range cst2Blocks {
		// Some blocks will return errors.
		_ = cst1.cs.AcceptBlock(block)
	}
	if cst1.cs.currentBlockID() != cst2.cs.currentBlockID() {
		t.Error("cst1 and cst2 do not share the same path")
	}
	if cst1.cs.consensusSetHash() != cst2.cs.consensusSetHash() {
		t.Error("cst1 and cst2 do not share the same consensus set hash")
	}

	// Mine 6 blocks on cst3 and then give those blocks to cst1, which will
	// cause cst1 to switch back to its old chain. cst1 will then have created,
	// reverted, and reapplied all the significant types of blocks.
	for i := 0; i < 6; i++ {
		block, _ := cst3.miner.FindBlock()
		err = cst3.cs.AcceptBlock(block)
		if err != nil {
			t.Fatal(err)
		}
	}
	var cst3Blocks []types.Block
	pb = cst3.cs.currentProcessedBlock()
	for pb.Block.ID() != cst3.cs.blockRoot.Block.ID() {
		cst3Blocks = append([]types.Block{pb.Block}, cst3Blocks...) // prepend
		pb = cst3.cs.db.getBlockMap(pb.Parent)
	}
	for _, block := range cst3Blocks {
		// Some blocks will return errors.
		err = cst1.cs.AcceptBlock(block)
		if err == nil {
		}
	}
	if cst1.cs.currentBlockID() != cst3.cs.currentBlockID() {
		t.Error("cst1 and cst3 do not share the same path")
	}
	if cst1.cs.consensusSetHash() != cst3.cs.consensusSetHash() {
		t.Error("cst1 and cst3 do not share the same consensus set hash")
	}
}

// TestBuriedBadFork creates a block with an invalid transaction that's not on
// the longest fork. The consensus set will not validate that block. Then valid
// blocks are added on top of it to make it the longest fork. When it becomes
// the longest fork, all the blocks should be fully validated and thrown out
// because a parent is invalid.
func TestBuriedBadFork(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	cst, err := createConsensusSetTester("TestBuriedBadFork")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()
	pb := cst.cs.currentProcessedBlock()

	// Create a bad block that builds on a parent, so that it is part of not
	// the longest fork.
	badBlock := types.Block{
		ParentID:     pb.Parent,
		Timestamp:    types.CurrentTimestamp(),
		MinerPayouts: []types.SiacoinOutput{{Value: types.CalculateCoinbase(pb.Height)}},
		Transactions: []types.Transaction{{
			SiacoinInputs: []types.SiacoinInput{{}}, // Will trigger an error on full verification but not partial verification.
		}},
	}
	parent := cst.cs.db.getBlockMap(pb.Parent)
	badBlock, _ = cst.miner.SolveBlock(badBlock, parent.ChildTarget)
	err = cst.cs.AcceptBlock(badBlock)
	if err != modules.ErrNonExtendingBlock {
		t.Fatal(err)
	}

	// Build another bock on top of the bad block that is fully valid, this
	// will cause a fork and full validation of the bad block, both the bad
	// block and this block should be thrown away.
	block := types.Block{
		ParentID:     badBlock.ID(),
		Timestamp:    types.CurrentTimestamp(),
		MinerPayouts: []types.SiacoinOutput{{Value: types.CalculateCoinbase(pb.Height + 1)}},
	}
	block, _ = cst.miner.SolveBlock(block, parent.ChildTarget) // okay because the target will not change
	err = cst.cs.AcceptBlock(block)
	if err == nil {
		t.Fatal("a bad block failed to cause an error")
	}
}

// TestBuriedBadTransaction tries submitting a block with a bad transaction
// that is buried under good transactions.
func TestBuriedBadTransaction(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestBuriedBadTransaction")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()
	pb := cst.cs.currentProcessedBlock()

	// Create a good transaction using the wallet.
	txnValue := types.NewCurrency64(1200)
	txnBuilder := cst.wallet.StartTransaction()
	err = txnBuilder.FundSiacoins(txnValue)
	if err != nil {
		t.Fatal(err)
	}
	txnBuilder.AddSiacoinOutput(types.SiacoinOutput{Value: txnValue})
	txnSet, err := txnBuilder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}
	err = cst.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}

	// Create a bad transaction
	badTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{}},
	}
	txns := append(cst.tpool.TransactionList(), badTxn)

	// Create a block with a buried bad transaction.
	block := types.Block{
		ParentID:     pb.Block.ID(),
		Timestamp:    types.CurrentTimestamp(),
		MinerPayouts: []types.SiacoinOutput{{Value: types.CalculateCoinbase(pb.Height + 1)}},
		Transactions: txns,
	}
	block, _ = cst.miner.SolveBlock(block, pb.ChildTarget)
	err = cst.cs.AcceptBlock(block)
	if err == nil {
		t.Error("buried transaction didn't cause an error")
	}
}

// COMPATv0.4.0
//
// This test checks that the hardfork scheduled for block 12,000 rolls through
// smoothly.
func TestTaxHardfork(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestTaxHardfork")
	if err != nil {
		t.Fatal(err)
	}
	defer cst.closeCst()

	// Create a file contract with a payout that is put into the blockchain
	// before the hardfork block but expires after the hardfork block.
	payout := types.NewCurrency64(400e6)
	fc := types.FileContract{
		WindowStart:        cst.cs.height() + 10,
		WindowEnd:          cst.cs.height() + 12,
		Payout:             payout,
		ValidProofOutputs:  []types.SiacoinOutput{{}},
		MissedProofOutputs: []types.SiacoinOutput{{}},
	}
	outputSize := payout.Sub(fc.Tax())
	fc.ValidProofOutputs[0].Value = outputSize
	fc.MissedProofOutputs[0].Value = outputSize

	// Create and fund a transaction with a file contract.
	txnBuilder := cst.wallet.StartTransaction()
	err = txnBuilder.FundSiacoins(payout)
	if err != nil {
		t.Fatal(err)
	}
	txnBuilder.AddFileContract(fc)
	txnSet, err := txnBuilder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}
	err = cst.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cst.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Check that the siafund pool was increased.
	var siafundPool types.Currency
	err = cst.cs.db.Update(func(tx *bolt.Tx) error {
		siafundPool = getSiafundPool(tx)
		return nil
	})
	if err != nil {
		panic(err)
	}

	// Check that the siafund pool did not change after the submitted revision.
	siafundPool = cst.cs.dbGetSiafundPool()
	if siafundPool.Cmp(types.NewCurrency64(15590e3)) != 0 {
		t.Fatal("siafund pool was not increased correctly")
	}
}
