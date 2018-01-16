package api_test

import (
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/vechain/thor/api"
	"github.com/vechain/thor/api/utils/types"
	"github.com/vechain/thor/block"
	"github.com/vechain/thor/chain"
	"github.com/vechain/thor/cry"
	"github.com/vechain/thor/genesis"
	"github.com/vechain/thor/lvldb"
	"github.com/vechain/thor/state"
	"github.com/vechain/thor/thor"
	"github.com/vechain/thor/tx"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBlock(t *testing.T) {

	block, ts := addBlock(t)
	raw := types.ConvertBlock(block)
	defer ts.Close()

	res, err := http.Get(ts.URL + fmt.Sprintf("/block/hash/%v", block.Hash().String()))
	if err != nil {
		t.Fatal(err)
	}
	r, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(r))
	rb := new(types.Block)
	if err := json.Unmarshal(r, &rb); err != nil {
		t.Fatal(err)
	}

	checkBlock(t, raw, rb)

	//get transaction from blocknumber with index
	res, err = http.Get(ts.URL + fmt.Sprintf("/block/number/%v", 1))
	if err != nil {
		t.Fatal(err)
	}
	r, err = ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	rb = new(types.Block)
	if err := json.Unmarshal(r, &rb); err != nil {
		t.Fatal(err)
	}

	checkBlock(t, raw, rb)

}

func addBlock(t *testing.T) (*block.Block, *httptest.Server) {
	db, _ := lvldb.NewMem()
	hash, _ := thor.ParseHash(emptyRootHash)
	s, _ := state.New(hash, db)
	chain := chain.New(db)
	bi := api.NewBlockInterface(chain)
	router := mux.NewRouter()
	api.NewBlockHTTPRouter(router, bi)
	ts := httptest.NewServer(router)

	b, err := genesis.Build(s)
	if err != nil {
		t.Fatal(err)
	}

	chain.WriteGenesis(b)
	key, _ := crypto.GenerateKey()
	address, _ := thor.ParseAddress(testAddress)
	cla := &tx.Clause{To: &address, Value: big.NewInt(10), Data: nil}
	genesisHash, _ := thor.ParseHash("0x000000006d2958e8510b1503f612894e9223936f1008be2a218c310fa8c11423")
	signing := cry.NewSigning(genesisHash)
	tx := new(tx.Builder).
		GasPrice(big.NewInt(1000)).
		Gas(1000).
		TimeBarrier(10000).
		Clause(cla).
		Nonce(1).
		Build()

	sig, _ := signing.Sign(tx, crypto.FromECDSA(key))
	tx = tx.WithSignature(sig)
	best, _ := chain.GetBestBlock()
	bl := new(block.Builder).
		ParentHash(best.Hash()).
		Transaction(tx).
		Build()
	if err := chain.AddBlock(bl, true); err != nil {
		t.Fatal(err)
	}

	return bl, ts
}

func checkBlock(t *testing.T, expBl *types.Block, actBl *types.Block) {
	assert.Equal(t, expBl.Number, actBl.Number, "Number should be equal")
	assert.Equal(t, expBl.Hash, actBl.Hash, "Hash should be equal")
	assert.Equal(t, expBl.ParentHash, actBl.ParentHash, "ParentHash should be equal")
	assert.Equal(t, expBl.Timestamp, actBl.Timestamp, "Timestamp should be equal")
	assert.Equal(t, expBl.TotalScore, actBl.TotalScore, "TotalScore should be equal")
	assert.Equal(t, expBl.GasLimit, actBl.GasLimit, "GasLimit should be equal")
	assert.Equal(t, expBl.GasUsed, actBl.GasUsed, "GasUsed should be equal")
	assert.Equal(t, expBl.Beneficiary, actBl.Beneficiary, "Beneficiary should be equal")
	assert.Equal(t, expBl.TxsRoot, actBl.TxsRoot, "TxsRoot should be equal")
	assert.Equal(t, expBl.StateRoot, actBl.StateRoot, "StateRoot should be equal")
	assert.Equal(t, expBl.ReceiptsRoot, actBl.ReceiptsRoot, "ReceiptsRoot should be equal")
	for i, txhash := range expBl.Txs {
		assert.Equal(t, txhash, actBl.Txs[i], "tx hash should be equal")
	}

}
