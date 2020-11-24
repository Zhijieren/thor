// Copyright (c) 2018 The VeChainThor developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package chain

import (
	"sync/atomic"

	"github.com/pkg/errors"
	"github.com/vechain/thor/block"
	"github.com/vechain/thor/co"
	"github.com/vechain/thor/kv"
	"github.com/vechain/thor/muxdb"
	"github.com/vechain/thor/thor"
	"github.com/vechain/thor/tx"
)

const (
	dataStoreName = "chain.data"
	propStoreName = "chain.props"
)

var (
	errNotFound    = errors.New("not found")
	bestBlockIDKey = []byte("best-block-id")
)

// Repository stores block headers, txs and receipts.
//
// It's thread-safe.
type Repository struct {
	db    *muxdb.MuxDB
	data  kv.Store
	props kv.Store

	genesis *block.Block
	best    atomic.Value
	tag     byte
	tick    co.Signal

	caches struct {
		summaries *cache
		txs       *cache
		receipts  *cache
		bss       *cache
	}
}

// NewRepository create an instance of repository.
func NewRepository(db *muxdb.MuxDB, genesis *block.Block) (*Repository, error) {
	if genesis.Header().Number() != 0 {
		return nil, errors.New("genesis number != 0")
	}
	if len(genesis.Transactions()) != 0 {
		return nil, errors.New("genesis block should not have transactions")
	}

	genesisID := genesis.Header().ID()
	repo := &Repository{
		db:      db,
		data:    db.NewStore(dataStoreName),
		props:   db.NewStore(propStoreName),
		genesis: genesis,
		tag:     genesisID[31],
	}

	repo.caches.summaries = newCache(512)
	repo.caches.txs = newCache(2048)
	repo.caches.receipts = newCache(2048)
	repo.caches.bss = newCache(512)

	if val, err := repo.props.Get(bestBlockIDKey); err != nil {
		if !repo.props.IsNotFound(err) {
			return nil, err
		}

		indexRoot, err := repo.indexBlock(thor.Bytes32{}, genesis, nil)
		if err != nil {
			return nil, err
		}
		if err := repo.saveBlock(genesis, nil, indexRoot); err != nil {
			return nil, err
		}
		if err := repo.setBestBlock(genesis); err != nil {
			return nil, err
		}
	} else {
		bestID := thor.BytesToBytes32(val)
		existingGenesisID, err := repo.NewChain(bestID).GetBlockID(0)
		if err != nil {
			return nil, errors.Wrap(err, "get existing genesis id")
		}
		if existingGenesisID != genesisID {
			return nil, errors.New("genesis mismatch")
		}

		b, err := repo.GetBlock(bestID)
		if err != nil {
			return nil, errors.Wrap(err, "get best block")
		}
		repo.best.Store(b)
	}

	return repo, nil
}

// ChainTag returns chain tag, which is the last byte of genesis id.
func (r *Repository) ChainTag() byte {
	return r.tag
}

// GenesisBlock returns genesis block.
func (r *Repository) GenesisBlock() *block.Block {
	return r.genesis
}

// BestBlock returns the best block, which is the newest block of canonical chain.
func (r *Repository) BestBlock() *block.Block {
	return r.best.Load().(*block.Block)
}

// SetBestBlockID set the given block id as best block id.
func (r *Repository) SetBestBlockID(id thor.Bytes32) (err error) {
	defer func() {
		if err == nil {
			r.tick.Broadcast()
		}
	}()
	b, err := r.GetBlock(id)
	if err != nil {
		return err
	}
	return r.setBestBlock(b)
}

func (r *Repository) setBestBlock(b *block.Block) error {
	if err := r.props.Put(bestBlockIDKey, b.Header().ID().Bytes()); err != nil {
		return err
	}
	r.best.Store(b)
	return nil
}

func (r *Repository) saveBlock(block *block.Block, receipts tx.Receipts, indexRoot thor.Bytes32) error {
	return r.data.Batch(func(putter kv.PutFlusher) error {
		var (
			header  = block.Header()
			id      = header.ID()
			txs     = block.Transactions()
			bss     = block.BackerSignatures()
			summary = BlockSummary{header, indexRoot, []thor.Bytes32{}, uint64(block.Size())}
		)

		if n := len(txs); n > 0 {
			key := makeTxKey(id, txInfix)
			for i, tx := range txs {
				key.SetIndex(uint64(i))
				if err := saveTransaction(putter, key, tx); err != nil {
					return err
				}
				r.caches.txs.Add(key, tx)
				summary.Txs = append(summary.Txs, tx.ID())
			}
			key = makeTxKey(id, receiptInfix)
			for i, receipt := range receipts {
				key.SetIndex(uint64(i))
				if err := saveReceipt(putter, key, receipt); err != nil {
					return err
				}
				r.caches.receipts.Add(key, receipt)
			}
		}
		if err := saveBackerSignatures(putter, id, bss); err != nil {
			return err
		}
		r.caches.bss.Add(id, bss)
		if err := saveBlockSummary(putter, &summary); err != nil {
			return err
		}
		r.caches.summaries.Add(id, &summary)
		return nil
	})
}

// AddBlock add a new block with its receipts into repository.
func (r *Repository) AddBlock(newBlock *block.Block, receipts tx.Receipts) error {
	parentSummary, err := r.GetBlockSummary(newBlock.Header().ParentID())
	if err != nil {
		if r.IsNotFound(err) {
			return errors.New("parent missing")
		}
		return err
	}
	indexRoot, err := r.indexBlock(parentSummary.IndexRoot, newBlock, receipts)
	if err != nil {
		return err
	}

	if err := r.saveBlock(newBlock, receipts, indexRoot); err != nil {
		return err
	}

	// Write the persistent branch head info
	if isBranchHead(r.data, newBlock.Header().ParentID()) {
		saveBranchHead(r.data, newBlock.Header().ID(), newBlock.Header().ParentID())
	} else {
		saveBranchHead(r.data, newBlock.Header().ID(), thor.Bytes32{})
	}

	return nil
}

// GetBlockSummary get block summary by block id.
func (r *Repository) GetBlockSummary(id thor.Bytes32) (summary *BlockSummary, err error) {
	var cached interface{}
	if cached, err = r.caches.summaries.GetOrLoad(id, func() (interface{}, error) {
		return loadBlockSummary(r.data, id)
	}); err != nil {
		return
	}
	return cached.(*BlockSummary), nil
}

func (r *Repository) getTransaction(key txKey) (*tx.Transaction, error) {
	cached, err := r.caches.txs.GetOrLoad(key, func() (interface{}, error) {
		return loadTransaction(r.data, key)
	})
	if err != nil {
		return nil, err
	}
	return cached.(*tx.Transaction), nil
}

// GetBlockTransactions get all transactions of the block for given block id.
func (r *Repository) GetBlockTransactions(id thor.Bytes32) (tx.Transactions, error) {
	summary, err := r.GetBlockSummary(id)
	if err != nil {
		return nil, err
	}

	if n := len(summary.Txs); n > 0 {
		txs := make(tx.Transactions, n)
		key := makeTxKey(id, txInfix)
		for i := range summary.Txs {
			key.SetIndex(uint64(i))
			txs[i], err = r.getTransaction(key)
			if err != nil {
				return nil, err
			}
		}
		return txs, nil
	}
	return nil, nil
}

// GetBlockBackerSignatures get all backer signatures of a block for given block id.
func (r *Repository) GetBlockBackerSignatures(id thor.Bytes32) (block.ComplexSignatures, error) {
	cached, err := r.caches.bss.GetOrLoad(id, func() (interface{}, error) {
		bss, err := loadBackerSignatures(r.data, id)
		if err != nil {
			// backward compatibility
			if r.IsNotFound(err) {
				return bss, nil
			}
			return nil, err
		}
		return bss, nil
	})
	if err != nil {
		return nil, err
	}
	return cached.(block.ComplexSignatures), nil
}

// GetBlock get block by id.
func (r *Repository) GetBlock(id thor.Bytes32) (*block.Block, error) {
	summary, err := r.GetBlockSummary(id)
	if err != nil {
		return nil, err
	}
	txs, err := r.GetBlockTransactions(id)
	if err != nil {
		return nil, err
	}
	bss, err := r.GetBlockBackerSignatures(id)
	if err != nil {
		return nil, err
	}
	return block.Compose(summary.Header, txs, bss), nil
}

func (r *Repository) getReceipt(key txKey) (*tx.Receipt, error) {
	cached, err := r.caches.receipts.GetOrLoad(key, func() (interface{}, error) {
		return loadReceipt(r.data, key)
	})
	if err != nil {
		return nil, err
	}
	return cached.(*tx.Receipt), nil
}

// GetBlockReceipts get all tx receipts of the block for given block id.
func (r *Repository) GetBlockReceipts(id thor.Bytes32) (tx.Receipts, error) {
	summary, err := r.GetBlockSummary(id)
	if err != nil {
		return nil, err
	}

	if n := len(summary.Txs); n > 0 {
		receipts := make(tx.Receipts, n)
		key := makeTxKey(id, receiptInfix)
		for i := range summary.Txs {
			key.SetIndex(uint64(i))
			receipts[i], err = r.getReceipt(key)
			if err != nil {
				return nil, err
			}
		}
		return receipts, nil
	}
	return nil, nil
}

// IsNotFound returns if the given error means not found.
func (r *Repository) IsNotFound(err error) bool {
	return err == errNotFound || r.db.IsNotFound(err)
}

// NewTicker create a signal Waiter to receive event that the best block changed.
func (r *Repository) NewTicker() co.Waiter {
	return r.tick.NewWaiter()
}

// GetBranchesByID returns all the branches that contain the block
func (r *Repository) GetBranchesByID(id thor.Bytes32) (branches []*Chain, err error) {
	heads := loadBranchHeads(r.data, block.Number(id))

	for _, head := range heads {
		c := newChain(r, head)
		if ok, err := c.HasBlock(id); err != nil {
			return nil, err
		} else if ok {
			branches = append(branches, c)
		}
	}

	return
}

// GetBranchesByTimestamp returns all the branches newer than the input timestamp
func (r *Repository) GetBranchesByTimestamp(ts uint64) (branches []*Chain, err error) {
	heads := loadBranchHeads(
		r.data,
		uint32((ts-r.genesis.Header().Timestamp())/thor.BlockInterval), // min block number
	)

	for _, head := range heads {
		summary, err := r.GetBlockSummary(head)
		if err != nil {
			return nil, err
		}

		if summary.Header.Timestamp() <= ts {
			continue
		}

		c := newChain(r, head)
		branches = append(branches, c)
	}

	return
}

// IfConflict checks whether the input two blocks conflict with each other
func (r *Repository) IfConflict(b1, b2 thor.Bytes32) (bool, error) {
	var (
		c         *Chain
		low, high thor.Bytes32
	)

	if _, err := r.GetBlockSummary(b1); err != nil {
		if r.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if _, err := r.GetBlockSummary(b2); err != nil {
		if r.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	if b1 == b2 {
		return false, nil
	}

	if block.Number(b1) == block.Number(b2) {
		return true, nil
	} else if block.Number(b1) > block.Number(b2) {
		// Here compare block number should be okay since
		// if the two blocks are on the same chain, the one
		// with a larger number is always newer than the other
		low, high = b2, b1
	} else {
		low, high = b1, b2
	}

	c = r.NewChain(high)
	ok, err := c.HasBlock(low)
	if err != nil {
		return true, err
	}
	return !ok, nil
}
