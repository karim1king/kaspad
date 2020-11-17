package consensusstatemanager

import (
	"github.com/kaspanet/kaspad/domain/consensus/model"
	"github.com/kaspanet/kaspad/domain/consensus/model/externalapi"
	"github.com/kaspanet/kaspad/domain/consensus/processes/consensusstatemanager/utxoalgebra"
	"github.com/kaspanet/kaspad/domain/consensus/ruleerrors"
	"github.com/pkg/errors"
)

func (csm *consensusStateManager) resolveBlockStatus(blockHash *externalapi.DomainHash) (externalapi.BlockStatus, error) {
	// get list of all blocks in the selected parent chain that have not yet resolved their status
	unverifiedBlocks, err := csm.getUnverifiedChainBlocks(blockHash)
	if err != nil {
		return 0, err
	}

	// If there's no unverified blocks in the given block's chain - this means the given block already has a
	// UTXO-verified status, and therefore it should be retrieved from the store and returned
	if len(unverifiedBlocks) == 0 {
		return csm.blockStatusStore.Get(csm.databaseContext, blockHash)
	}

	selectedParentStatus, err := csm.findSelectedParentStatus(unverifiedBlocks)
	if err != nil {
		return 0, err
	}

	var blockStatus externalapi.BlockStatus
	// resolve the unverified blocks' statuses in opposite order
	for i := len(unverifiedBlocks) - 1; i >= 0; i-- {
		unverifiedBlockHash := unverifiedBlocks[i]

		if selectedParentStatus == externalapi.StatusDisqualifiedFromChain {
			blockStatus = externalapi.StatusDisqualifiedFromChain
		} else {
			blockStatus, err = csm.resolveSingleBlockStatus(unverifiedBlockHash)
			if err != nil {
				return 0, err
			}
		}

		csm.blockStatusStore.Stage(unverifiedBlockHash, blockStatus)
		selectedParentStatus = blockStatus
	}

	return blockStatus, nil
}

// findSelectedParentStatus returns the status of the selectedParent of the last block in the unverifiedBlocks chain
func (csm *consensusStateManager) findSelectedParentStatus(unverifiedBlocks []*externalapi.DomainHash) (
	externalapi.BlockStatus, error) {
	lastUnverifiedBlock := unverifiedBlocks[len(unverifiedBlocks)-1]
	if *lastUnverifiedBlock == *csm.genesisHash {
		return externalapi.StatusValid, nil
	}
	lastUnverifiedBlockGHOSTDAGData, err := csm.ghostdagDataStore.Get(csm.databaseContext, lastUnverifiedBlock)
	if err != nil {
		return 0, err
	}
	return csm.blockStatusStore.Get(csm.databaseContext, lastUnverifiedBlockGHOSTDAGData.SelectedParent)
}

func (csm *consensusStateManager) getUnverifiedChainBlocks(
	blockHash *externalapi.DomainHash) ([]*externalapi.DomainHash, error) {

	unverifiedBlocks := []*externalapi.DomainHash{}
	currentHash := blockHash
	for {
		currentBlockStatus, err := csm.blockStatusStore.Get(csm.databaseContext, currentHash)
		if err != nil {
			return nil, err
		}
		if currentBlockStatus != externalapi.StatusUTXOPendingVerification {
			return unverifiedBlocks, nil
		}

		unverifiedBlocks = append(unverifiedBlocks, currentHash)

		currentBlockGHOSTDAGData, err := csm.ghostdagDataStore.Get(csm.databaseContext, currentHash)
		if err != nil {
			return nil, err
		}

		if currentBlockGHOSTDAGData.SelectedParent == nil {
			return unverifiedBlocks, nil // this means we reached genesis
		}

		currentHash = currentBlockGHOSTDAGData.SelectedParent
	}
}

func (csm *consensusStateManager) resolveSingleBlockStatus(blockHash *externalapi.DomainHash) (externalapi.BlockStatus, error) {
	pastUTXODiff, acceptanceData, multiset, err := csm.CalculatePastUTXOAndAcceptanceData(blockHash)
	if err != nil {
		return 0, err
	}

	err = csm.acceptanceDataStore.Stage(blockHash, acceptanceData)
	if err != nil {
		return 0, err
	}

	block, err := csm.blockStore.Block(csm.databaseContext, blockHash)
	if err != nil {
		return 0, err
	}

	err = csm.verifyUTXO(block, blockHash, pastUTXODiff, acceptanceData, multiset)
	if err != nil {
		if errors.As(err, &ruleerrors.RuleError{}) {
			return externalapi.StatusDisqualifiedFromChain, nil
		}
		return 0, err
	}

	csm.multisetStore.Stage(blockHash, multiset)
	err = csm.utxoDiffStore.Stage(blockHash, pastUTXODiff, nil)
	if err != nil {
		return 0, err
	}

	err = csm.updateParentDiffs(blockHash, pastUTXODiff)
	if err != nil {
		return 0, err
	}

	return externalapi.StatusValid, nil
}
func (csm *consensusStateManager) updateParentDiffs(
	blockHash *externalapi.DomainHash, pastUTXODiff *model.UTXODiff) error {
	parentHashes, err := csm.dagTopologyManager.Parents(blockHash)
	if err != nil {
		return err
	}
	for _, parentHash := range parentHashes {
		// skip all parents that already have a utxo-diff child
		parentHasUTXODiffChild, err := csm.utxoDiffStore.HasUTXODiffChild(csm.databaseContext, parentHash)
		if err != nil {
			return err
		}
		if parentHasUTXODiffChild {
			continue
		}

		parentStatus, err := csm.blockStatusStore.Get(csm.databaseContext, parentHash)
		if err != nil {
			return err
		}
		if parentStatus != externalapi.StatusValid {
			continue
		}

		// parents that till now didn't have a utxo-diff child - were actually virtual's diffParents.
		// Update them to have the new block as their utxo-diff child
		parentCurrentDiff, err := csm.utxoDiffStore.UTXODiff(csm.databaseContext, parentHash)
		if err != nil {
			return err
		}
		parentNewDiff, err := utxoalgebra.DiffFrom(pastUTXODiff, parentCurrentDiff)
		if err != nil {
			return err
		}

		err = csm.utxoDiffStore.Stage(parentHash, parentNewDiff, blockHash)
		if err != nil {
			return err
		}
	}

	return nil
}