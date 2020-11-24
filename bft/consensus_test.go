package bft

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vechain/thor/block"
	"github.com/vechain/thor/chain"
	"github.com/vechain/thor/thor"
)

func printFinalityState(n uint32, v *view, cons *Consensus) {
	fmt.Printf("Blk%d, sigSize = %d, finalVec = [ %d, %d, %d, %d, %d ]\n",
		n,
		len(v.nv),
		block.Number(cons.state[NV]),
		block.Number(cons.state[PP]),
		block.Number(cons.state[PC]),
		block.Number(cons.state[CM]),
		block.Number(cons.state[FN]),
	)
}

func TestNormalSituation(t *testing.T) {
	/**
	Genesis -- b1
			|
			|-------c1
	*/
	var (
		head         *block.Block
		branch       *chain.Chain
		v            *view
		err          error
		maxBlockNum  uint32 = 20
		expNumBacker        = 40
		N                   = int(thor.MaxBlockProposers)

		leader  int
		backers []int

		cons      *Consensus
		nv        thor.Bytes32
		prevState [5]thor.Bytes32
	)

	repo := newTestRepo()
	head = repo.GenesisBlock()

	// node := pubToAddr(nodes[rand.Intn(N)].PublicKey)
	node := nodeAddress(rand.Intn(N))
	cons = NewConsensus(repo, head.Header().ID(), node)

	branch = repo.NewChain(head.Header().ID())
	v, err = newView(branch, block.Number(head.Header().NV()))
	assert.Nil(t, err)

	for {
		// Randomly pick the leader and backers
		leader = rand.Intn(N)
		backers = nil
		for i := 0; i < N; i++ {
			if rand.Intn(N) < expNumBacker {
				backers = append(backers, i)
			}
		}

		// Use the new block's ID as the NV value if the block is the first block of its view
		nv = cons.state[NV]
		if v.hasQCForNV() || head.Header().Number() == 0 {
			nv = GenNVForFirstBlock(head.Header().Number() + 1)
		}

		head = newBlock(
			leader, backers, head, 1,
			[4]thor.Bytes32{
				nv,
				cons.state[PP],
				cons.state[PC],
				cons.state[CM],
			},
		)

		// Check whether node is either the leader or a backer
		// If yes, update lastSignedPC
		ifUpdateLastSignedPC := false
		if !cons.state[PC].IsZero() {
			if nodeAddress(leader) == node {
				ifUpdateLastSignedPC = true
			} else {
				for _, backer := range backers {
					if nodeAddress(backer) == node {
						ifUpdateLastSignedPC = true
					}
				}
			}
		}
		if ifUpdateLastSignedPC {
			assert.Nil(t, cons.UpdateLastSignedPC(head.Header()))
		}

		repo.AddBlock(head, nil)
		repo.SetBestBlockID(head.Header().ID())

		prevState = cons.Get()
		cons.Update(head)

		// In case of a block starting a new view, check the NV value change
		if v.hasQCForNV() {
			expected := prevState
			expected[NV] = head.Header().ID()
			assert.Equal(t, expected, cons.Get())
		}

		branch = repo.NewChain(head.Header().ID())
		v, err = newView(branch, block.Number(head.Header().NV()))
		assert.Nil(t, err)

		// In case of a view with QC, PP <- NV, PC <- PP, FN <- new PC
		if v.hasQCForNV() {
			expected := prevState
			expected[NV] = prevState[NV]
			expected[PP] = prevState[NV]
			expected[PC] = prevState[PP]
			expected[CM] = prevState[PC]
			if !expected[CM].IsZero() {
				expected[FN] = expected[CM]
			}
			assert.Equal(t, expected, cons.Get())
		}

		printFinalityState(head.Header().Number(), v, cons)

		if head.Header().Number() >= maxBlockNum {
			break
		}
	}
}

func Test1b(t *testing.T) {
	/**
	Genesis --- b1
			|
			|------- c1
	*/
	repo := newTestRepo()
	gen := repo.BestBlock()
	cons := NewConsensus(repo, gen.Header().ID(), pubToAddr(nodes[0].PublicKey))

	b1 := newBlock(
		0, nil, gen, 1,
		[4]thor.Bytes32{GenNVForFirstBlock(1)},
	)

	c1 := newBlock(
		1, nil, gen, 2,
		[4]thor.Bytes32{GenNVForFirstBlock(1)},
	)

	assert.Nil(t, repo.AddBlock(b1, nil))
	assert.Nil(t, repo.AddBlock(c1, nil))

	assert.Nil(t, cons.repo.SetBestBlockID(b1.Header().ID()))
	assert.Nil(t, cons.Update(b1))
	assert.Equal(t, b1.Header().ID(), cons.state[NV])

	assert.Nil(t, cons.repo.SetBestBlockID(c1.Header().ID()))
	assert.Nil(t, cons.Update(c1))
	assert.Equal(t, c1.Header().ID(), cons.state[NV])
}

func Test1c(t *testing.T) {
	/**
	Genesis -------- b1
			|
			|-- c1 ------ c2
	*/
	repo := newTestRepo()
	gen := repo.BestBlock()
	cons := NewConsensus(repo, gen.Header().ID(), pubToAddr(nodes[0].PublicKey))

	b1 := newBlock(
		0, nil, gen, 2,
		[4]thor.Bytes32{GenNVForFirstBlock(1)},
	)
	c1 := newBlock(
		0, nil, gen, 1,
		[4]thor.Bytes32{GenNVForFirstBlock(1)},
	)
	c2 := newBlock(
		0, nil, c1, 2,
		[4]thor.Bytes32{c1.Header().ID()},
	)

	assert.Nil(t, repo.AddBlock(b1, nil))
	assert.Nil(t, repo.AddBlock(c1, nil))
	assert.Nil(t, repo.AddBlock(c2, nil))

	assert.Nil(t, cons.repo.SetBestBlockID(b1.Header().ID()))

	assert.Nil(t, cons.Update(b1))
	assert.Equal(t, b1.Header().ID(), cons.state[NV])

	assert.Nil(t, cons.Update(c1))
	assert.Equal(t, b1.Header().ID(), cons.state[NV])

	assert.Nil(t, cons.repo.SetBestBlockID(c2.Header().ID()))

	assert.Nil(t, cons.Update(c2))
	assert.Equal(t, c1.Header().ID(), cons.state[NV])
}

func TestNVHasQC(t *testing.T) {
	/**
	Genesis --- b1 --- b2

	b1 has a QC
	nv[b1] <- b1
	nv[b2] <- b1
	*/

	repo := newTestRepo()
	gen := repo.BestBlock()
	cons := NewConsensus(repo, gen.Header().ID(), pubToAddr(nodes[0].PublicKey))

	b1 := newBlock(
		0, inds[:QC+3], gen, 1,
		[4]thor.Bytes32{GenNVForFirstBlock(1)},
	)

	b2 := newBlock(
		0, inds[:1], b1, 1,
		[4]thor.Bytes32{b1.Header().ID()},
	)

	assert.Nil(t, repo.AddBlock(b1, nil))
	assert.Nil(t, repo.AddBlock(b2, nil))

	assert.Nil(t, cons.repo.SetBestBlockID(b2.Header().ID()))

	cons.state[NV] = b1.Header().ID()
	assert.Nil(t, cons.Update(b2))
	assert.Equal(t, b2.Header().ID(), cons.state[NV])
}

func TestPPUnlock(t *testing.T) {
	/**
	Genesis --- b1
			|
			|------- c1
	*/

	repo := newTestRepo()
	gen := repo.BestBlock()
	cons := NewConsensus(repo, gen.Header().ID(), pubToAddr(nodes[0].PublicKey))

	b1 := newBlock(
		0, nil, gen, 1,
		[4]thor.Bytes32{GenNVForFirstBlock(1)},
	)

	c1 := newBlock(
		1, nil, gen, 2,
		[4]thor.Bytes32{GenNVForFirstBlock(1)},
	)

	assert.Nil(t, repo.AddBlock(b1, nil))
	assert.Nil(t, repo.AddBlock(c1, nil))

	cons.state[NV] = b1.Header().ID()
	cons.state[PP] = b1.Header().ID()

	assert.Nil(t, cons.repo.SetBestBlockID(c1.Header().ID()))
	assert.Nil(t, cons.Update(c1))
	assert.True(t, cons.state[PP].IsZero())
}

func Test3g(t *testing.T) {

}

func TestCM(t *testing.T) {
	repo := newTestRepo()
	gen := repo.BestBlock()
	cons := NewConsensus(repo, gen.Header().ID(), pubToAddr(nodes[0].PublicKey))

	leader := 0
	backers := inds[:QC]

	var blocks []*block.Block
	blocks = append(blocks, gen)

	emptyBytes32 := thor.Bytes32{}
	for i := 1; i <= 3; i++ {
		fv := [4]thor.Bytes32{GenNVForFirstBlock(uint32(i))}
		if i == 3 {
			fv = [4]thor.Bytes32{
				GenNVForFirstBlock(uint32(i)),
				emptyBytes32,
				emptyBytes32,
				blocks[2].Header().ID(),
			}
		}

		b := newBlock(leader, backers, blocks[i-1], 1, fv)
		assert.Nil(t, repo.AddBlock(b, nil))
		blocks = append(blocks, b)
	}

	// Observed CM not newer than the locally committed
	cons.state[CM] = blocks[3].Header().ID()
	assert.Nil(t, cons.Update(blocks[3]))
	assert.Equal(t, blocks[3].Header().ID(), cons.state[CM])

	// Observed CM newer than the locally committed
	cons.state = [5]thor.Bytes32{}
	cons.state[CM] = blocks[1].Header().ID()
	assert.Nil(t, cons.Update(blocks[3]))
	assert.Equal(t, blocks[2].Header().ID(), cons.state[CM])
}
