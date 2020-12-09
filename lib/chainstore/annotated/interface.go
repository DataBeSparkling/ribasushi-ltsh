package annotated

import (
	"context"
	"sync"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v4/pgxpool"

	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/lib/blockstore"
	ipfsblock "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
)

type Chainstore interface {
	blockstore.Blockstore
	blockstore.Viewer
	SetCurrentTipset(context.Context, *types.TipSet) (didChange bool, err error)
}

type acs struct {
	linearSyncEventCount int64
	cacheSize            int64
	cache                *ristretto.Cache
	currentTipset        *types.TipSet
	dbPool               *pgxpool.Pool
	accessStatsRecent    map[uint64]struct{}
	accessStatsHiRes     map[accessUnit]uint64
	limiterSetLastAccess chan struct{}
	limiterBlockParse    chan struct{}
	limiterCompress      chan struct{}
	mu                   sync.Mutex
}

type blockUnit struct {
	size              uint32
	cid               cid.Cid
	dbID              *uint64
	compressedContent []byte
	hydratedBlock     ipfsblock.Block
	mu                sync.Mutex
	errHolder         error
	parsedLinks       []cid.Cid
}
type accessType uint8

const (
	MASKTYPE = 0b11

	PUT  = accessType(0)
	GET  = accessType(1)
	HAS  = accessType(2)
	SIZE = accessType(3)

	// cache-modifier
	PREEXISTING = accessType(1 << 6) // db R/W access skipped due to cache hit or already-existing entry
)

type accessUnit struct {
	atUnix     time.Time
	dbID       uint64
	accessType accessType
}

//
// Unimplemented
func (*acs) DeleteBlock(cid.Cid) error {
	panic("DeleteBlock is not implemented by the annotated blockstore")
}
func (*acs) AllKeysChan(context.Context) (<-chan cid.Cid, error) {
	panic("AllKeysChan is not implemented by the annotated blockstore")
}
func (*acs) HashOnRead(bool) {} // just noop: we always hash

//
// Writers
func (cs *acs) Put(b ipfsblock.Block) error         { return cs.dbPut([]ipfsblock.Block{b}) }
func (cs *acs) PutMany(bls []ipfsblock.Block) error { return cs.dbPut(bls) }

//
// Readers
func (cs *acs) Has(c cid.Cid) (found bool, err error) {
	var bu *blockUnit
	bu, err = cs.dbGet(c, HAS)
	if bu != nil && err == nil {
		found = true
	}
	return
}

func (cs *acs) GetSize(c cid.Cid) (int, error) {
	bu, err := cs.dbGet(c, SIZE)

	switch {

	case err != nil:
		return -1, err

	case bu == nil:
		return -1, blockstore.ErrNotFound

	default:
		return int(bu.size), nil
	}
}

func (cs *acs) Get(c cid.Cid) (ipfsblock.Block, error) {
	bu, err := cs.dbGet(c, GET)

	switch {

	case err != nil:
		return nil, err

	case bu == nil:
		return nil, blockstore.ErrNotFound

	default:
		return bu.block()
	}
}

func (cs *acs) View(c cid.Cid, cb func([]byte) error) error {
	blk, err := cs.Get(c)
	if err != nil {
		return err
	}
	return cb(blk.RawData())
}
