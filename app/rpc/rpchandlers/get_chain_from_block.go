package rpchandlers

import (
	"github.com/kaspanet/kaspad/app/appmessage"
	"github.com/kaspanet/kaspad/app/rpc/rpccontext"
	"github.com/kaspanet/kaspad/domain/consensus/model/externalapi"
	"github.com/kaspanet/kaspad/domain/consensus/utils/hashes"
	"github.com/kaspanet/kaspad/infrastructure/network/netadapter/router"
	"github.com/pkg/errors"
)

const (
	// maxBlocksInGetChainFromBlockResponse is the max amount of blocks that
	// are allowed in a GetChainFromBlockResponse.
	maxBlocksInGetChainFromBlockResponse = 1000
)

// HandleGetChainFromBlock handles the respectively named RPC command
func HandleGetChainFromBlock(context *rpccontext.Context, _ *router.Router, request appmessage.Message) (appmessage.Message, error) {
	getChainFromBlockRequest := request.(*appmessage.GetChainFromBlockRequestMessage)
	var startHash *externalapi.DomainHash
	if getChainFromBlockRequest.StartHash != "" {
		var err error
		startHash, err = hashes.FromString(getChainFromBlockRequest.StartHash)
		if err != nil {
			errorMessage := &appmessage.GetChainFromBlockResponseMessage{}
			errorMessage.Error = appmessage.RPCErrorf("Could not parse startHash: %s", err)
			return errorMessage, nil
		}

		_, err = context.Domain.Consensus().GetBlock(startHash)
		if err != nil {
			errorMessage := &appmessage.GetChainFromBlockResponseMessage{}
			errorMessage.Error = appmessage.RPCErrorf("Block %s not found", startHash)
			return errorMessage, nil
		}
	}

	// Retrieve the selected parent chain.
	removedChainHashes, addedChainHashes, err := context.Domain.Consensus().GetSelectedParentChain(startHash)
	if err != nil {
		return nil, err
	}

	// Limit the amount of blocks in the response
	if len(addedChainHashes) > maxBlocksInGetChainFromBlockResponse {
		addedChainHashes = addedChainHashes[:maxBlocksInGetChainFromBlockResponse]
	}

	// Collect addedChainBlocks.
	addedChainBlocks, err := CollectChainBlocks(context, addedChainHashes)
	if err != nil {
		errorMessage := &appmessage.GetChainFromBlockResponseMessage{}
		errorMessage.Error = appmessage.RPCErrorf("Could not collect chain blocks: %s", err)
		return errorMessage, nil
	}

	// Collect removedHashes.
	removedHashes := make([]string, len(removedChainHashes))
	for i, hash := range removedChainHashes {
		removedHashes[i] = hash.String()
	}

	// If the user specified to include the blocks, collect them as well.
	var blockVerboseData []*appmessage.BlockVerboseData
	if getChainFromBlockRequest.IncludeBlockVerboseData {
		data, err := hashesToBlockVerboseData(context, addedChainHashes)
		if err != nil {
			return nil, err
		}
		blockVerboseData = data
	}

	response := appmessage.NewGetChainFromBlockResponseMessage(removedHashes, addedChainBlocks, blockVerboseData)
	return response, nil
}

// hashesToBlockVerboseData takes block hashes and returns their
// correspondent block verbose.
func hashesToBlockVerboseData(context *rpccontext.Context, hashes []*externalapi.DomainHash) ([]*appmessage.BlockVerboseData, error) {
	getBlockVerboseResults := make([]*appmessage.BlockVerboseData, 0, len(hashes))
	for _, blockHash := range hashes {
		block, err := context.Domain.Consensus().GetBlock(blockHash)
		if err != nil {
			return nil, errors.Errorf("could not retrieve block %s.", blockHash)
		}
		getBlockVerboseResult, err := context.BuildBlockVerboseData(block, false)
		if err != nil {
			return nil, errors.Wrapf(err, "could not build getBlockVerboseResult for block %s", blockHash)
		}
		getBlockVerboseResults = append(getBlockVerboseResults, getBlockVerboseResult)
	}
	return getBlockVerboseResults, nil
}

// CollectChainBlocks creates a slice of chain blocks from the given hashes
func CollectChainBlocks(context *rpccontext.Context, hashes []*externalapi.DomainHash) ([]*appmessage.ChainBlock, error) {
	chainBlocks := make([]*appmessage.ChainBlock, 0, len(hashes))
	for _, hash := range hashes {
		acceptanceData, err := context.Domain.Consensus().GetBlockAcceptanceData(hash)
		if err != nil {
			return nil, errors.Errorf("could not retrieve acceptance data for block %s", hash)
		}

		acceptedBlocks := make([]*appmessage.AcceptedBlock, 0, len(acceptanceData))
		for _, blockAcceptanceData := range acceptanceData {
			acceptedTxIds := make([]string, 0, len(blockAcceptanceData.TransactionAcceptanceData))
			for _, txAcceptanceData := range blockAcceptanceData.TransactionAcceptanceData {
				if txAcceptanceData.IsAccepted {
					acceptedTxIds = append(acceptedTxIds, txAcceptanceData.Transaction.ID.String())
				}
			}
			acceptedBlock := &appmessage.AcceptedBlock{
				Hash:          blockAcceptanceData.BlockHash.String(),
				AcceptedTxIDs: acceptedTxIds,
			}
			acceptedBlocks = append(acceptedBlocks, acceptedBlock)
		}

		chainBlock := &appmessage.ChainBlock{
			Hash:           hash.String(),
			AcceptedBlocks: acceptedBlocks,
		}
		chainBlocks = append(chainBlocks, chainBlock)
	}
	return chainBlocks, nil
}
