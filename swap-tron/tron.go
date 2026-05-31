package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Base58 alphabet (same as Bitcoin)
const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func b58Decode(input string) []byte {
	n := big.NewInt(0)
	for _, c := range input {
		idx := strings.IndexRune(b58Alphabet, c)
		if idx < 0 {
			return nil
		}
		n.Mul(n, big.NewInt(58))
		n.Add(n, big.NewInt(int64(idx)))
	}
	b := n.Bytes()
	// Add leading zero bytes for each leading '1'
	leadingZeros := 0
	for i := 0; i < len(input) && input[i] == '1'; i++ {
		leadingZeros++
	}
	b = append(bytes.Repeat([]byte{0}, leadingZeros), b...)
	return b
}

func mustDecodeAddress(base58 string) common.Address {
	return EthAddressFromTron(Base58ToHex(base58))
}

// Base58ToHex converts TRON base58 address to hex string (with 0x41 prefix)
func Base58ToHex(addr string) string {
	decoded := b58Decode(addr)
	if len(decoded) < 4 {
		return ""
	}
	// Remove last 4 checksum bytes
	payload := decoded[:len(decoded)-4]
	return hex.EncodeToString(payload)
}

// EthAddressFromTron extracts the 20-byte Ethereum address from a TRON hex address
func EthAddressFromTron(tronHex string) common.Address {
	b, _ := hex.DecodeString(strings.TrimPrefix(tronHex, "0x"))
	if len(b) == 21 && b[0] == 0x41 {
		return common.BytesToAddress(b[1:])
	}
	return common.BytesToAddress(b)
}

// ---------------------------------------------------------------------------
// TRON HTTP client
// ---------------------------------------------------------------------------

type TronClient struct {
	apiURL  string
	httpCli *http.Client
}

func NewTronClient(apiURL string) *TronClient {
	if apiURL == "" {
		apiURL = "https://api.trongrid.io"
	}
	return &TronClient{
		apiURL:  strings.TrimRight(apiURL, "/"),
		httpCli: &http.Client{},
	}
}

// ---------------------------------------------------------------------------
// Transaction types
// ---------------------------------------------------------------------------

type Transaction struct {
	Visible    bool              `json:"visible"`
	TxID       string            `json:"txID"`
	RawData    TransactionRawData `json:"raw_data"`
	RawDataHex string            `json:"raw_data_hex"`
	Signature  []string          `json:"signature,omitempty"`
}

type TransactionRawData struct {
	Contract      []Contract    `json:"contract"`
	RefBlockBytes string        `json:"ref_block_bytes"`
	RefBlockHash  string        `json:"ref_block_hash"`
	Expiration    int64         `json:"expiration"`
	Timestamp     int64         `json:"timestamp"`
	FeeLimit      int64         `json:"fee_limit"`
}

type Contract struct {
	Parameter ContractParameter `json:"parameter"`
	Type      string            `json:"type"`
}

type ContractParameter struct {
	Value   ContractValue `json:"value"`
	TypeURL string        `json:"type_url"`
}

type ContractValue struct {
	Data            string `json:"data"`
	OwnerAddress    string `json:"owner_address"`
	ContractAddress string `json:"contract_address"`
	CallValue       int64  `json:"call_value,omitempty"`
}

// ---------------------------------------------------------------------------
// TriggerSmartContract – builds unsigned transaction
// ---------------------------------------------------------------------------

type TriggerRequest struct {
	OwnerAddress    string `json:"owner_address"`
	ContractAddress string `json:"contract_address"`
	Data            string `json:"data"`
	CallValue       int64  `json:"call_value,omitempty"`
	FeeLimit        int64  `json:"fee_limit"`
	Visible         bool   `json:"visible"`
}

type TriggerResult struct {
	Result bool `json:"result"`
}

type TriggerResponse struct {
	Result        *TriggerResult `json:"result"`
	Transaction   *Transaction   `json:"transaction"`
	EnergyUsed    int64          `json:"energy_used,omitempty"`
	EnergyPenalty int64          `json:"energy_penalty,omitempty"`
	ConstantResult []string      `json:"constant_result,omitempty"`
	Logs          []any          `json:"logs,omitempty"`
	Message       string         `json:"message,omitempty"`
}

func (c *TronClient) TriggerSmartContract(req TriggerRequest) (*Transaction, error) {
	body, _ := json.Marshal(req)
	resp, err := c.httpCli.Post(c.apiURL+"/wallet/triggersmartcontract",
		"application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var tr TriggerResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("triggersmartcontract parse: %w\nbody: %s", err, string(respBody))
	}

	if tr.Result == nil || !tr.Result.Result {
		msg := tr.Message
		if b, err := hex.DecodeString(msg); err == nil {
			msg = string(b)
		}
		return nil, fmt.Errorf("triggersmartcontract failed: %s", msg)
	}
	if tr.Transaction == nil {
		return nil, fmt.Errorf("triggersmartcontract returned nil transaction:\n%s", string(respBody))
	}
	return tr.Transaction, nil
}

// ---------------------------------------------------------------------------
// Signing
// ---------------------------------------------------------------------------

// SignTransaction signs the txID (SHA256 of raw_data) with the ECDSA private key
// and appends the signature to the transaction.
func SignTransaction(tx *Transaction, privKeyHex string) error {
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(privKeyHex, "0x"))
	if err != nil {
		return fmt.Errorf("invalid private key: %w", err)
	}

	txIDBytes, err := hex.DecodeString(tx.TxID)
	if err != nil {
		return fmt.Errorf("decode txID: %w", err)
	}

	sig, err := crypto.Sign(txIDBytes, privKey)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	// TRON uses recovery id 0/1, go-ethereum returns 27/28
	if sig[64] >= 27 {
		sig[64] -= 27
	}

	tx.Signature = []string{hex.EncodeToString(sig)}
	return nil
}

// ---------------------------------------------------------------------------
// Broadcast transaction
// ---------------------------------------------------------------------------

func (c *TronClient) BroadcastTransaction(tx *Transaction) (string, error) {
	body, _ := json.Marshal(tx)
	resp, err := c.httpCli.Post(c.apiURL+"/wallet/broadcasttransaction",
		"application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Result  bool   `json:"result"`
		TxID    string `json:"txid"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("broadcast parse: %w\nbody: %s", err, string(respBody))
	}

	if !result.Result {
		msg := result.Message
		if b, err := hex.DecodeString(msg); err == nil {
			msg = string(b)
		}
		return "", fmt.Errorf("broadcast failed: %s", msg)
	}
	return result.TxID, nil
}
