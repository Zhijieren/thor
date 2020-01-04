package consensus

import (
	"encoding/binary"
	"math"

	"github.com/vechain/thor/thor"
	"github.com/vechain/thor/vrf"
)

// Role consensus role type
type Role uint8

// Define different roles
const (
	Leader Role = iota
	Committee
	Normal
)

func getCommitteeThreshold() uint64 {
	// threshold = (1 << 64 - 1) * committee_size / total_number_of_nodes * factor
	return uint64(math.MaxUint32) / thor.MaxBlockProposers * thor.CommitteeSize * thor.CommitteeThresholdFactor
}

// IsCommittee checks committeeship. proof == nil -> false, otherwise true.
func IsCommittee(sk *vrf.PrivateKey, seed thor.Bytes32) (*vrf.Proof, error) {
	// Compute VRF proof
	proof, err := sk.Prove(seed.Bytes())
	if err != nil {
		return nil, err
	}

	// Compute the hash of the proof
	h := thor.Blake2b(proof[:])
	// Get the threshold
	th := getCommitteeThreshold()
	// Is a committee member if the hash is no larger than the threshold
	if binary.BigEndian.Uint64(h.Bytes()) <= th {
		return proof, nil
	}
	return nil, nil
}