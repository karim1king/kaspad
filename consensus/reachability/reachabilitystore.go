package reachability

import (
	"bytes"
	"github.com/kaspanet/kaspad/consensus/blocknode"
	"github.com/kaspanet/kaspad/database"
	"github.com/kaspanet/kaspad/dbaccess"
	"github.com/kaspanet/kaspad/util/daghash"
	"github.com/kaspanet/kaspad/wire"
	"github.com/pkg/errors"
	"io"
)

type reachabilityData struct {
	treeNode          *reachabilityTreeNode
	futureCoveringSet futureCoveringTreeNodeSet
}

type reachabilityStore struct {
	blockNodeStore *blocknode.BlockNodeStore
	dirty          map[daghash.Hash]struct{}
	loaded         map[daghash.Hash]*reachabilityData
}

func newReachabilityStore(blockNodeStore *blocknode.BlockNodeStore) *reachabilityStore {
	return &reachabilityStore{
		blockNodeStore: blockNodeStore,
		dirty:          make(map[daghash.Hash]struct{}),
		loaded:         make(map[daghash.Hash]*reachabilityData),
	}
}

func (store *reachabilityStore) setTreeNode(treeNode *reachabilityTreeNode) {
	// load the reachability data from DB to store.loaded
	node := treeNode.blockNode
	_, exists := store.reachabilityDataByHash(node.Hash())
	if !exists {
		store.loaded[*node.Hash()] = &reachabilityData{}
	}

	store.loaded[*node.Hash()].treeNode = treeNode
	store.setBlockAsDirty(node.Hash())
}

func (store *reachabilityStore) setFutureCoveringSet(node *blocknode.BlockNode, futureCoveringSet futureCoveringTreeNodeSet) error {
	// load the reachability data from DB to store.loaded
	_, exists := store.reachabilityDataByHash(node.Hash())
	if !exists {
		return reachabilityNotFoundError(node.Hash())
	}

	store.loaded[*node.Hash()].futureCoveringSet = futureCoveringSet
	store.setBlockAsDirty(node.Hash())
	return nil
}

func (store *reachabilityStore) setBlockAsDirty(blockHash *daghash.Hash) {
	store.dirty[*blockHash] = struct{}{}
}

func reachabilityNotFoundError(hash *daghash.Hash) error {
	return errors.Errorf("couldn't find reachability data for block %s", hash)
}

func (store *reachabilityStore) treeNodeByBlockHash(hash *daghash.Hash) (*reachabilityTreeNode, error) {
	reachabilityData, exists := store.reachabilityDataByHash(hash)
	if !exists {
		return nil, reachabilityNotFoundError(hash)
	}
	return reachabilityData.treeNode, nil
}

func (store *reachabilityStore) treeNodeByBlockNode(node *blocknode.BlockNode) (*reachabilityTreeNode, error) {
	return store.treeNodeByBlockHash(node.Hash())
}

func (store *reachabilityStore) futureCoveringSetByBlockNode(node *blocknode.BlockNode) (futureCoveringTreeNodeSet, error) {
	reachabilityData, exists := store.reachabilityDataByHash(node.Hash())
	if !exists {
		return nil, reachabilityNotFoundError(node.Hash())
	}
	return reachabilityData.futureCoveringSet, nil
}

func (store *reachabilityStore) reachabilityDataByHash(hash *daghash.Hash) (*reachabilityData, bool) {
	reachabilityData, ok := store.loaded[*hash]
	return reachabilityData, ok
}

// flushToDB writes all dirty reachability data to the database.
func (store *reachabilityStore) flushToDB(dbContext *dbaccess.TxContext) error {
	if len(store.dirty) == 0 {
		return nil
	}

	for hash := range store.dirty {
		hash := hash // Copy hash to a new variable to avoid passing the same pointer
		reachabilityData := store.loaded[hash]
		err := store.storeReachabilityData(dbContext, &hash, reachabilityData)
		if err != nil {
			return err
		}
	}
	return nil
}

func (store *reachabilityStore) clearDirtyEntries() {
	store.dirty = make(map[daghash.Hash]struct{})
}

func (store *reachabilityStore) init(dbContext dbaccess.Context) error {
	// TODO: (Stas) This is a quick and dirty hack.
	// We iterate over the entire bucket twice:
	// * First, populate the loaded set with all entries
	// * Second, connect the parent/children pointers in each entry
	//   with other nodes, which are now guaranteed to exist
	cursor, err := dbaccess.ReachabilityDataCursor(dbContext)
	if err != nil {
		return err
	}
	defer cursor.Close()

	for ok := cursor.First(); ok; ok = cursor.Next() {
		err := store.initReachabilityData(cursor)
		if err != nil {
			return err
		}
	}

	for ok := cursor.First(); ok; ok = cursor.Next() {
		err := store.loadReachabilityDataFromCursor(cursor)
		if err != nil {
			return err
		}
	}
	return nil
}

func (store *reachabilityStore) initReachabilityData(cursor database.Cursor) error {
	key, err := cursor.Key()
	if err != nil {
		return err
	}

	hash, err := daghash.NewHash(key.Suffix())
	if err != nil {
		return err
	}

	store.loaded[*hash] = &reachabilityData{
		treeNode:          &reachabilityTreeNode{},
		futureCoveringSet: nil,
	}
	return nil
}

func (store *reachabilityStore) loadReachabilityDataFromCursor(cursor database.Cursor) error {
	key, err := cursor.Key()
	if err != nil {
		return err
	}

	hash, err := daghash.NewHash(key.Suffix())
	if err != nil {
		return err
	}

	reachabilityData, ok := store.reachabilityDataByHash(hash)
	if !ok {
		return errors.Errorf("cannot find reachability data for block hash: %s", hash)
	}

	serializedReachabilityData, err := cursor.Value()
	if err != nil {
		return err
	}

	err = store.deserializeReachabilityData(serializedReachabilityData, reachabilityData)
	if err != nil {
		return err
	}

	// Connect the treeNode with its BlockNode
	reachabilityData.treeNode.blockNode, ok = store.blockNodeStore.LookupNode(hash)
	if !ok {
		return errors.Errorf("block %s does not exist in the DAG", hash)
	}

	return nil
}

// storeReachabilityData stores the reachability data to the database.
// This overwrites the current entry if there exists one.
func (store *reachabilityStore) storeReachabilityData(dbContext dbaccess.Context, hash *daghash.Hash, reachabilityData *reachabilityData) error {
	serializedReachabilyData, err := store.serializeReachabilityData(reachabilityData)
	if err != nil {
		return err
	}

	return dbaccess.StoreReachabilityData(dbContext, hash, serializedReachabilyData)
}

func (store *reachabilityStore) serializeReachabilityData(reachabilityData *reachabilityData) ([]byte, error) {
	w := &bytes.Buffer{}
	err := store.serializeTreeNode(w, reachabilityData.treeNode)
	if err != nil {
		return nil, err
	}
	err = store.serializeFutureCoveringSet(w, reachabilityData.futureCoveringSet)
	if err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

func (store *reachabilityStore) serializeTreeNode(w io.Writer, treeNode *reachabilityTreeNode) error {
	// Serialize the interval
	err := store.serializeReachabilityInterval(w, treeNode.interval)
	if err != nil {
		return err
	}

	// Serialize the parent
	// If this is the genesis block, write the zero hash instead
	parentHash := &daghash.ZeroHash
	if treeNode.parent != nil {
		parentHash = treeNode.parent.blockNode.Hash()
	}
	err = wire.WriteElement(w, parentHash)
	if err != nil {
		return err
	}

	// Serialize the amount of children
	err = wire.WriteVarInt(w, uint64(len(treeNode.children)))
	if err != nil {
		return err
	}

	// Serialize the children
	for _, child := range treeNode.children {
		err = wire.WriteElement(w, child.blockNode.Hash())
		if err != nil {
			return err
		}
	}

	return nil
}

func (store *reachabilityStore) serializeReachabilityInterval(w io.Writer, interval *reachabilityInterval) error {
	// Serialize start
	err := wire.WriteElement(w, interval.start)
	if err != nil {
		return err
	}

	// Serialize end
	err = wire.WriteElement(w, interval.end)
	if err != nil {
		return err
	}

	return nil
}

func (store *reachabilityStore) serializeFutureCoveringSet(w io.Writer, futureCoveringSet futureCoveringTreeNodeSet) error {
	// Serialize the set size
	err := wire.WriteVarInt(w, uint64(len(futureCoveringSet)))
	if err != nil {
		return err
	}

	// Serialize each node in the set
	for _, node := range futureCoveringSet {
		err = wire.WriteElement(w, node.blockNode.Hash())
		if err != nil {
			return err
		}
	}

	return nil
}

func (store *reachabilityStore) deserializeReachabilityData(
	serializedReachabilityDataBytes []byte, destination *reachabilityData) error {

	r := bytes.NewBuffer(serializedReachabilityDataBytes)

	// Deserialize the tree node
	err := store.deserializeTreeNode(r, destination)
	if err != nil {
		return err
	}

	// Deserialize the future covering set
	err = store.deserializeFutureCoveringSet(r, destination)
	if err != nil {
		return err
	}

	return nil
}

func (store *reachabilityStore) deserializeTreeNode(r io.Reader, destination *reachabilityData) error {
	// Deserialize the interval
	interval, err := store.deserializeReachabilityInterval(r)
	if err != nil {
		return err
	}
	destination.treeNode.interval = interval

	// Deserialize the parent
	// If this is the zero hash, this node is the genesis and as such doesn't have a parent
	parentHash := &daghash.Hash{}
	err = wire.ReadElement(r, parentHash)
	if err != nil {
		return err
	}
	if !daghash.ZeroHash.IsEqual(parentHash) {
		parentReachabilityData, ok := store.reachabilityDataByHash(parentHash)
		if !ok {
			return errors.Errorf("parent reachability data not found for hash: %s", parentHash)
		}
		destination.treeNode.parent = parentReachabilityData.treeNode
	}

	// Deserialize the amount of children
	childCount, err := wire.ReadVarInt(r)
	if err != nil {
		return err
	}

	// Deserialize the children
	children := make([]*reachabilityTreeNode, childCount)
	for i := uint64(0); i < childCount; i++ {
		childHash := &daghash.Hash{}
		err = wire.ReadElement(r, childHash)
		if err != nil {
			return err
		}
		childReachabilityData, ok := store.reachabilityDataByHash(childHash)
		if !ok {
			return errors.Errorf("child reachability data not found for hash: %s", parentHash)
		}
		children[i] = childReachabilityData.treeNode
	}
	destination.treeNode.children = children

	return nil
}

func (store *reachabilityStore) deserializeReachabilityInterval(r io.Reader) (*reachabilityInterval, error) {
	interval := &reachabilityInterval{}

	// Deserialize start
	start := uint64(0)
	err := wire.ReadElement(r, &start)
	if err != nil {
		return nil, err
	}
	interval.start = start

	// Deserialize end
	end := uint64(0)
	err = wire.ReadElement(r, &end)
	if err != nil {
		return nil, err
	}
	interval.end = end

	return interval, nil
}

func (store *reachabilityStore) deserializeFutureCoveringSet(r io.Reader, destination *reachabilityData) error {
	// Deserialize the set size
	setSize, err := wire.ReadVarInt(r)
	if err != nil {
		return err
	}

	// Deserialize each block in the set
	futureCoveringSet := make(futureCoveringTreeNodeSet, setSize)
	for i := uint64(0); i < setSize; i++ {
		blockHash := &daghash.Hash{}
		err = wire.ReadElement(r, blockHash)
		if err != nil {
			return err
		}
		blockReachabilityData, ok := store.reachabilityDataByHash(blockHash)
		if !ok {
			return errors.Errorf("block reachability data not found for hash: %s", blockHash)
		}
		futureCoveringSet[i] = blockReachabilityData.treeNode
	}
	destination.futureCoveringSet = futureCoveringSet

	return nil
}