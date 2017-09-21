package blockchain_test

import (
	"fmt"
	"io/ioutil"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tendermint/go-wire/data"
	"github.com/tendermint/tendermint/blockchain"
	"github.com/tendermint/tendermint/types"
	"github.com/tendermint/tmlibs/db"
)

func TestLoadBlockStoreStateJSON(t *testing.T) {
	db := db.NewMemDB()

	bsj := &blockchain.BlockStoreStateJSON{Height: 1000}
	bsj.Save(db)

	retrBSJ := blockchain.LoadBlockStoreStateJSON(db)

	assert.Equal(t, *bsj, retrBSJ, "expected the retrieved DBs to match")
}

var blockStoreKey = []byte("blockStore")

func TestNewBlockStore(t *testing.T) {
	db := db.NewMemDB()
	db.Set(blockStoreKey, []byte(`{"height": 10000}`))
	bs := blockchain.NewBlockStore(db)
	assert.Equal(t, bs.Height(), 10000, "failed to properly parse blockstore")

	panicCausers := [][]byte{
		[]byte("artful-doger"),
		[]byte(""),
		[]byte(" "),
	}

	for i, panicCauser := range panicCausers {
		// Expecting a panic here on trying to parse an invalid blockStore
		_, _, err := doFn(func() (interface{}, error) {
			db.Set(blockStoreKey, panicCauser)
			_ = blockchain.NewBlockStore(db)
			return nil, nil
		})
		assert.NotNil(t, err, "#%d panicCauser: %q expected a panic", i, panicCauser)
		// Note: We can't check for the exact substring because
		// recovered stack traces as of Sept 2017 seem to
		// mask the message. See https://play.golang.org/p/W9NTc4Fstr
		// TODO: Perhaps file an error on the Go project.
	}

	db.Set(blockStoreKey, nil)
	bs = blockchain.NewBlockStore(db)
	assert.Equal(t, bs.Height(), 0, "expecting nil bytes to be unmarshaled alright")
}

func TestBlockStoreGetReader(t *testing.T) {
	db := db.NewMemDB()
	// Initial setup
	db.Set([]byte("Foo"), []byte("Bar"))
	db.Set([]byte("Foo1"), nil)

	bs := blockchain.NewBlockStore(db)

	tests := [...]struct {
		key  []byte
		want []byte
	}{
		0: {key: []byte("Foo"), want: []byte("Bar")},
		1: {key: []byte("KnoxNonExistent"), want: nil},
		2: {key: []byte("Foo1"), want: nil},
	}

	for i, tt := range tests {
		r := bs.GetReader(tt.key)
		if r == nil {
			assert.Nil(t, tt.want, "#%d: expected a non-nil reader", i)
			continue
		}
		slurp, err := ioutil.ReadAll(r)
		if err != nil {
			t.Errorf("#%d: unexpected Read err: %v", i, err)
		} else {
			assert.Equal(t, slurp, tt.want, "#%d: mismatch", i)
		}
	}
}

func freshBlockStore() (*blockchain.BlockStore, db.DB) {
	db := db.NewMemDB()
	return blockchain.NewBlockStore(db), db
}

func TestBlockStoreSaveLoadBlock(t *testing.T) {
	bs, _ := freshBlockStore()
	noBlockHeights := []int{0, -1, 100, 1000, 2}
	for i, height := range noBlockHeights {
		if g := bs.LoadBlock(height); g != nil {
			t.Errorf("#%d: height(%d) got a block; want nil", i, height)
		}
	}

	// Setup, test data
	part2 := &types.Part{Index: 1, Bytes: data.Bytes([]byte{
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x01, 0x01, 0x00,
		0x00, 0x01, 0x0a, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	})}
	part1 := &types.Part{Index: 0, Bytes: data.Bytes([]byte{
		0x01, 0x01, 0x01, 0x0a, 0x62, 0x6c, 0x6f, 0x63, 0x6b, 0x5f, 0x74, 0x65,
		0x73, 0x74, 0x01, 0x01, 0xa1, 0xb2, 0x03, 0xeb, 0x3d, 0x1f, 0x44, 0x40, 0x01, 0x64, 0x00,
	})}
	validPartSet := types.NewPartSetFromHeader(types.PartSetHeader{Total: 2})
	validPartSet.AddPart(part1, false)
	validPartSet.AddPart(part2, false)

	incompletePartSet := types.NewPartSetFromHeader(types.PartSetHeader{Total: 2})

	uncontiguousPartSet := types.NewPartSetFromHeader(types.PartSetHeader{Total: 0})
	uncontiguousPartSet.AddPart(part2, false)
	seenCommit1 := &types.Commit{Precommits: []*types.Vote{{Height: 10}}}

	// End of setup, test data

	tuples := []struct {
		block            *types.Block
		parts            *types.PartSet
		seenCommit       *types.Commit
		wantErr          bool
		wantPanic        bool
		eraseDBForCommit bool
		skipReason       string
	}{
		{
			block: &types.Block{
				Header: &types.Header{
					Height:  1,
					NumTxs:  100,
					ChainID: "block_test",
				},
				LastCommit: &types.Commit{Precommits: []*types.Vote{{Height: 10}}},
			},
			parts:      validPartSet,
			seenCommit: seenCommit1,
		},

		{
			block:     nil,
			wantPanic: true, // want a panic on saving nil block
		},

		{
			block: &types.Block{
				Header: &types.Header{
					Height:  4,
					NumTxs:  100,
					ChainID: "block_test",
				},
				LastCommit: &types.Commit{Precommits: []*types.Vote{{Height: 10}}},
			},
			parts:     uncontiguousPartSet,
			wantPanic: true, // and incomplete and uncontiguous parts
		},

		{
			block: &types.Block{
				Header: &types.Header{
					Height:  1,
					NumTxs:  100,
					ChainID: "block_test",
				},
				LastCommit: &types.Commit{Precommits: []*types.Vote{{Height: 10}}},
			},
			parts:     incompletePartSet,
			wantPanic: true, // incomplete parts
		},

		{
			block: &types.Block{
				Header: &types.Header{
					Height:  1,
					NumTxs:  100,
					ChainID: "block_test",
				},
				LastCommit: &types.Commit{Precommits: []*types.Vote{{Height: 10}}},
			},
			parts:            validPartSet,
			seenCommit:       seenCommit1,
			eraseDBForCommit: true,
			skipReason:       "Waiting on https://github.com/tendermint/tmlibs/pull/56 to be merged first",
			wantPanic:        true, // erase all content so that the commit will not be read
		},
	}

	type quad struct {
		block  *types.Block
		commit *types.Commit
		meta   *types.BlockMeta

		seenCommit *types.Commit
	}

	for i, tuple := range tuples {
		if tuple.skipReason != "" {
			t.Logf("#%d: skipping this test case because of reason:\n%s", i, tuple.skipReason)
			continue
		}
		bs, db := freshBlockStore()
		// SaveBlock
		res, err, panicErr := doFn(func() (interface{}, error) {
			bs.SaveBlock(tuple.block, tuple.parts, tuple.seenCommit)
			if tuple.block == nil {
				return nil, nil
			}
			bBlock := bs.LoadBlock(tuple.block.Height)
			bBlockMeta := bs.LoadBlockMeta(tuple.block.Height)
			bSeenCommit := bs.LoadSeenCommit(tuple.block.Height)
			if tuple.eraseDBForCommit {
				db.Close()
			}
			bCommit := bs.LoadBlockCommit(tuple.block.Height - 1)
			return &quad{block: bBlock, seenCommit: bSeenCommit, commit: bCommit, meta: bBlockMeta}, nil
		})

		if tuple.wantPanic {
			if panicErr == nil {
				t.Errorf("#%d: want a non-nil panic", i)
			}
			continue
		}

		if tuple.wantErr {
			if err == nil {
				t.Errorf("#%d: got nil error", i)
			}
			continue
		}

		assert.Nil(t, panicErr, "#%d: unexpected panic", i)
		assert.Nil(t, err, "#%d: expecting a non-nil error", i)
		qua, ok := res.(*quad)
		if !ok || qua == nil {
			t.Errorf("#%d: got nil quad back; gotType=%T", i, res)
			continue
		}
	}
}

func doFn(fn func() (interface{}, error)) (res interface{}, err error, panicErr error) {
	defer func() {
		if r := recover(); r != nil {
			stack := make([]byte, 1024)
			_ = runtime.Stack(stack, true)
			panicErr = fmt.Errorf("%s", stack)
		}
	}()

	res, err = fn()
	return res, err, panicErr
}
