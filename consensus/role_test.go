package consensus

import (
	"bytes"
	"crypto/rand"
	"math"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/vechain/thor/block"
	"github.com/vechain/thor/thor"
	"github.com/vechain/thor/vrf"
)

func TestThreshold(t *testing.T) {
	th := getCommitteeThreshold()
	// ratio = threhsold / (1 << 32 - 1) <= amp_factor * #committee / #node
	ratio := float64(th) / float64(math.MaxUint32)
	if ratio > float64(thor.CommitteeSize)/float64(thor.MaxBlockProposers)*float64(thor.CommitteeThresholdFactor) {
		t.Errorf("Invalid threshold")
	}
}

func TestIsCommitteeByPrivateKey(t *testing.T) {
	_, sk := vrf.GenKeyPair()

	// th := getCommitteeThreshold()

	var (
		msg       = make([]byte, 32)
		proof, pf *vrf.Proof
		err       error
		ok        bool
	)

	// Get a positive sample
	for {
		rand.Read(msg)
		proof, err = sk.Prove(msg)
		if err != nil {
			t.Error(err)
		}

		if isCommitteeByProof(proof) {
			break
		}
	}

	ok, pf, err = isCommitteeByPrivateKey(sk, thor.BytesToBytes32(msg))
	if err != nil || !ok || pf == nil || bytes.Compare(pf[:], proof[:]) != 0 {
		t.Errorf("Testing positive sample failed")
	}

	// Get a negative sample
	for {
		rand.Read(msg)
		proof, err = sk.Prove(msg)
		if err != nil {
			t.Error(err)
		}

		if !isCommitteeByProof(proof) {
			break
		}
	}

	ok, pf, err = isCommitteeByPrivateKey(sk, thor.BytesToBytes32(msg))
	if err != nil || ok || pf != nil {
		t.Errorf("Testing negative sample failed")
	}
}

func M(a ...interface{}) []interface{} {
	return a
}

func TestEpochNumber(t *testing.T) {
	cons := initConsensus()
	launchTime := cons.chain.GenesisBlock().Header().Timestamp()

	tests := []struct {
		expected interface{}
		returned interface{}
		msg      string
	}{
		{
			[]interface{}{uint32(0), errTimestamp},
			M(cons.EpochNumber(launchTime - 1)),
			"t < launch_time",
		},
		{
			[]interface{}{uint32(0), nil},
			M(cons.EpochNumber(launchTime + 1)),
			"t = launch_time + 1",
		},
		{
			[]interface{}{uint32(1), nil},
			M(cons.EpochNumber(launchTime + thor.BlockInterval)),
			"t = launch_time + block_interval",
		},
		{
			[]interface{}{uint32(1), nil},
			M(cons.EpochNumber(launchTime + thor.BlockInterval*thor.EpochInterval)),
			"t = launch_time + block_interval * epoch_interval",
		},
		{
			[]interface{}{uint32(1), nil},
			M(cons.EpochNumber(launchTime + thor.BlockInterval*thor.EpochInterval + 1)),
			"t = launch_time + block_interval * epoch_interval + 1",
		},
		{
			[]interface{}{uint32(2), nil},
			M(cons.EpochNumber(launchTime + thor.BlockInterval*(thor.EpochInterval+1))),
			"t = launch_time + block_interval * (epoch_interval + 1)",
		},
	}

	for _, test := range tests {
		assert.Equal(t, test.expected, test.returned, test.msg)
	}
}

func TestValidateBlockSummary(t *testing.T) {
	privateKey, _ := crypto.GenerateKey()

	cons := initConsensus()
	nRound := uint32(10)
	addEmptyBlocks(cons.chain, privateKey, nRound, make(map[uint32]interface{}))

	best := cons.chain.BestBlock()
	round := nRound + 1

	type testObj struct {
		ParentID  thor.Bytes32
		TxRoot    thor.Bytes32
		Timestamp uint64
	}

	tests := []struct {
		input testObj
		ret   error
		msg   string
	}{
		{
			testObj{best.Header().ID(), thor.Bytes32{}, cons.Timestamp(round)},
			nil,
			"clean case",
		},
		{
			testObj{best.Header().ParentID(), thor.Bytes32{}, cons.Timestamp(round)},
			errParent,
			"Invalid parent ID",
		},
		{
			testObj{best.Header().ID(), thor.Bytes32{}, cons.Timestamp(round) - 1},
			errTimestamp,
			"Invalid timestamp",
		},
	}

	for _, test := range tests {
		bs := block.NewBlockSummary(test.input.ParentID, test.input.TxRoot, test.input.Timestamp)
		sig, _ := crypto.Sign(bs.SigningHash().Bytes(), privateKey)
		bs = bs.WithSignature(sig)
		assert.Equal(t, cons.ValidateBlockSummary(bs), test.ret, test.msg)
	}
}

func getValidCommittee(seed thor.Bytes32) (*vrf.Proof, *vrf.PublicKey) {
	maxIter := 1000
	for i := 0; i < maxIter; i++ {
		pk, sk := vrf.GenKeyPair()
		proof, _ := sk.Prove(seed.Bytes())
		if isCommitteeByProof(proof) {
			return proof, pk
		}
	}
	return nil, nil
}

func getInvalidCommittee(seed thor.Bytes32) (*vrf.Proof, *vrf.PublicKey) {
	maxIter := 1000
	for i := 0; i < maxIter; i++ {
		pk, sk := vrf.GenKeyPair()
		proof, _ := sk.Prove(seed.Bytes())
		if !isCommitteeByProof(proof) {
			return proof, pk
		}
	}
	return nil, nil
}

func TestValidateEndorsement(t *testing.T) {
	ethsk, _ := crypto.GenerateKey()

	cons := initConsensus()
	gen := cons.chain.GenesisBlock().Header()

	// Create a valid block summary at round 1
	bs := block.NewBlockSummary(gen.ID(), thor.Bytes32{}, gen.Timestamp()+thor.BlockInterval)
	sig, err := crypto.Sign(bs.SigningHash().Bytes(), ethsk)
	if err != nil {
		t.Fatal(err)
	}
	bs = bs.WithSignature(sig)

	// Get the committee keys and proof
	beacon := getBeaconFromHeader(cons.chain.GenesisBlock().Header())
	seed := seed(beacon, 1)

	proof, pk := getValidCommittee(seed)
	if proof == nil {
		t.Errorf("Failed to find a valid committee")
	}

	_proof, _pk := getInvalidCommittee(seed)
	if proof == nil {
		t.Errorf("Failed to find a false committee")
	}

	var randKey vrf.PublicKey
	rand.Read(randKey[:])

	var randProof vrf.Proof
	rand.Read(randProof[:])

	type testObj struct {
		Summary   *block.Summary
		Proof     *vrf.Proof
		PublicKey *vrf.PublicKey
	}

	tests := []struct {
		input testObj
		ret   error
		msg   string
	}{
		{
			testObj{bs, proof, pk},
			nil,
			"clean case",
		},
		{
			testObj{bs, proof, &randKey},
			errVrfProof,
			"Random vrf public key",
		},
		{
			testObj{bs, &randProof, pk},
			errVrfProof,
			"Random vrf proof",
		},
		{
			testObj{bs, _proof, _pk},
			errNotCommittee,
			"Not committee",
		},
	}

	for _, test := range tests {
		ed := block.NewEndorsement(test.input.Summary, test.input.Proof)
		sig, _ = crypto.Sign(ed.SigningHash().Bytes(), ethsk)
		ed = ed.WithSignature(sig)
		assert.Equal(t, cons.ValidateEndorsement(ed, test.input.PublicKey), test.ret, test.msg)
	}
}

func BenchmarkTestEthSig(b *testing.B) {
	sk, _ := crypto.GenerateKey()

	msg := make([]byte, 32)

	for i := 0; i < b.N; i++ {
		rand.Read(msg)
		crypto.Sign(msg, sk)
	}
}

func BenchmarkBeacon(b *testing.B) {
	cons := initConsensus()

	for i := 0; i < b.N; i++ {
		cons.beacon(uint32(i + 1))
	}
}