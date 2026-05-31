package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// ---------------------------------------------------------------------------
// Token / contract config
// ---------------------------------------------------------------------------

const (
	SUNSWAP_QUOTE_API = "https://xxxx/swap/router"
	SUNSWAP_SOURCE    = "SUNSWAP_V3"

	// ---- user-specified addresses (DO NOT CHANGE) ----
	TRON_WTRX        = "TNUC9Qb1rRpS5CbWLmNMxXBjyFoydXjWFR"
	TRON_USDT        = "TMwFHYXLJaRUPeW6421aqXL4ZEzPRFGkGT" // USDJ
	UNIVERSAL_ROUTER = "TQqgNg13s2DjvXhW1ky4v6TsR8wZGvb7Y4"
	// ----------------------------------------------------

	DECIMALS_TRX  = 6
	DECIMALS_USDJ = 18
)

// ---------------------------------------------------------------------------
// Quote API types
// ---------------------------------------------------------------------------

type QuoteAPIResponse struct {
	Code    int              `json:"code"`
	Message string           `json:"message"`
	Data    []QuoteRouteData `json:"data"`
}

type QuoteRouteData struct {
	AmountIn       string   `json:"amountIn"`
	AmountOut      string   `json:"amountOut"`
	InUsd          string   `json:"inUsd"`
	OutUsd         string   `json:"outUsd"`
	Impact         string   `json:"impact"`
	Fee            string   `json:"fee"`
	Tokens         []string `json:"tokens"`
	Symbols        []string `json:"symbols"`
	PoolFees       []string `json:"poolFees"`
	PoolVersions   []string `json:"poolVersions"`
	StepAmountsOut []string `json:"stepAmountsOut"`
}

type GetQuoteResult struct {
	AmountOut    string
	AmountOutRaw *big.Int
	Fee          string
	PoolFee      string
}

// GetQuote fetches the best route from the SunSwap router API.
// outDecimals is the decimals of the output token (6 for TRX, 18 for USDJ).
func GetQuote(url string, outDecimals uint) (*GetQuoteResult, error) {
	cli := &http.Client{Timeout: 15 * time.Second}
	resp, err := cli.Get(url)
	if err != nil {
		return nil, fmt.Errorf("quote request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read quote body: %w", err)
	}

	var apiResp QuoteAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse API response: %w\nbody: %s", err, truncate(string(body), 500))
	}
	if apiResp.Code != 0 {
		return nil, fmt.Errorf("API error: code=%d msg=%s", apiResp.Code, apiResp.Message)
	}
	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("no routes: %s", truncate(string(body), 500))
	}

	best := apiResp.Data[0]
	amountOutRaw, err := decimalToRaw(best.AmountOut, outDecimals)
	if err != nil {
		return nil, fmt.Errorf("parse amountOut '%s': %w", best.AmountOut, err)
	}

	poolFee := "500"
	if len(best.PoolFees) > 0 {
		poolFee = best.PoolFees[0]
	}
	return &GetQuoteResult{
		AmountOut:    best.AmountOut,
		AmountOutRaw: amountOutRaw,
		Fee:          best.Fee,
		PoolFee:      poolFee,
	}, nil
}

func decimalToRaw(s string, decimals uint) (*big.Int, error) {
	parts := strings.SplitN(s, ".", 2)
	intPart := parts[0]
	fracPart := ""
	if len(parts) == 2 {
		fracPart = parts[1]
	}
	if len(fracPart) > int(decimals) {
		fracPart = fracPart[:decimals]
	}
	fracPart = fmt.Sprintf("%-*s", decimals, fracPart)
	fracPart = strings.ReplaceAll(fracPart, " ", "0")
	raw := new(big.Int)
	raw.SetString(intPart+fracPart, 10)
	return raw, nil
}

func BuildQuoteURL(fromToken, toToken string, amountIn *big.Int) string {
	return fmt.Sprintf("%s?fromToken=%s&toToken=%s&amountIn=%s&%s",
		SUNSWAP_QUOTE_API, fromToken, toToken, amountIn.String(), SUNSWAP_SOURCE)
}

// ---------------------------------------------------------------------------
// TRX -> Token  (WTRX -> USDJ)
// ---------------------------------------------------------------------------

func SwapTRXForToken(
	tronCli *TronClient,
	privateKeyHex string,
	ownerBase58 string,
	trxAmountSun *big.Int,
	slippageBips uint64,
	feeLimitSun int64,
	dryRun bool,
) error {
	ownerEth := mustDecodeAddress(ownerBase58)

	// ------- Step 1: Get quote -------
	quoteURL := BuildQuoteURL(TRON_WTRX, TRON_USDT, trxAmountSun)
	fmt.Printf("\nFetching quote: %s\n", quoteURL)

	quote, err := GetQuote(quoteURL, DECIMALS_USDJ)
	if err != nil {
		return fmt.Errorf("get quote: %w", err)
	}

	fmt.Printf("Quote: %s TRX -> %s Token (fee: %s%%)\n",
		toDecimal(trxAmountSun, DECIMALS_TRX), quote.AmountOut, quote.Fee)

	amountOutMin := new(big.Int).Mul(quote.AmountOutRaw, new(big.Int).SetUint64(10000-slippageBips))
	amountOutMin.Div(amountOutMin, big.NewInt(10000))
	fmt.Printf("Min out (%.2f%% slippage): %s Token\n",
		float64(slippageBips)/100.0, toDecimal(amountOutMin, DECIMALS_USDJ))

	// ------- Step 2: Build commands -------
	wtrxAddr := mustDecodeAddress(TRON_WTRX)
	destAddr := mustDecodeAddress(TRON_USDT)
	routerAddr := mustDecodeAddress(UNIVERSAL_ROUTER)

	// WRAP_ETH (0x0b): wrap TRX -> WTRX, send to router
	wrapInput := EncodeWrapETH(routerAddr, trxAmountSun)

	// V3_SWAP_EXACT_IN (0x00): swap WTRX -> USDJ
	// payerIsUser=false: router already holds WTRX from wrap step
	v3Input := EncodeV3SwapExactIn(
		ownerEth,
		wtrxAddr,
		destAddr,
		v3FeeFromPoolFee(quote.PoolFee),
		trxAmountSun,
		amountOutMin,
		false,
	)

	commands := []byte{CmdWrapETH, CmdV3SwapExactIn}
	inputs := [][]byte{wrapInput, v3Input}
	deadline := uint64(time.Now().Unix() + 600)
	calldata := BuildExecuteCalldata(commands, inputs, deadline)

	// ------- Step 3: Build tx -------
	ownerHex := Base58ToHex(ownerBase58)
	routerHex := Base58ToHex(UNIVERSAL_ROUTER)

	unsignedTx, err := tronCli.TriggerSmartContract(TriggerRequest{
		OwnerAddress:    ownerHex,
		ContractAddress: routerHex,
		Data:            fmt.Sprintf("%x", calldata),
		CallValue:       trxAmountSun.Int64(),
		FeeLimit:        feeLimitSun,
		Visible:         false,
	})
	if err != nil {
		return fmt.Errorf("build tx: %w", err)
	}
	fmt.Printf("Unsigned TXID: %s\n", unsignedTx.TxID)

	if dryRun {
		fmt.Println("Dry run: done (not signed / broadcast)")
		return nil
	}

	// ------- Step 4: Sign -------
	if err := SignTransaction(unsignedTx, privateKeyHex); err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	fmt.Printf("Signature: %s...\n", unsignedTx.Signature[0][:40])

	// ------- Step 5: Broadcast -------
	txID, err := tronCli.BroadcastTransaction(unsignedTx)
	if err != nil {
		return fmt.Errorf("broadcast: %w", err)
	}
	fmt.Printf("Swap submitted! TXID: %s\n", txID)
	fmt.Printf("View: https://tronscan.org/#/transaction/%s\n", txID)
	return nil
}

// ---------------------------------------------------------------------------
// Token -> TRX  (USDJ -> WTRX -> TRX)
// ---------------------------------------------------------------------------

// ApproveTokenForRouter builds an approval tx so the router can spend tokens.
func ApproveTokenForRouter(
	tronCli *TronClient,
	privateKeyHex string,
	ownerBase58 string,
	tokenBase58 string,
	amount *big.Int,
	feeLimitSun int64,
	dryRun bool,
) error {
	ownerHex := Base58ToHex(ownerBase58)
	tokenHex := Base58ToHex(tokenBase58)

	routerAddr := mustDecodeAddress(UNIVERSAL_ROUTER)
	approveData := BuildApproveCalldata(routerAddr, amount)

	unsignedTx, err := tronCli.TriggerSmartContract(TriggerRequest{
		OwnerAddress:    ownerHex,
		ContractAddress: tokenHex,
		Data:            fmt.Sprintf("%x", approveData),
		CallValue:       0,
		FeeLimit:        feeLimitSun,
		Visible:         false,
	})
	if err != nil {
		return fmt.Errorf("build approve tx: %w", err)
	}
	fmt.Printf("Unsigned approve TXID: %s\n", unsignedTx.TxID)

	if dryRun {
		fmt.Println("Dry run: approve tx not signed")
		return nil
	}

	if err := SignTransaction(unsignedTx, privateKeyHex); err != nil {
		return fmt.Errorf("sign approve: %w", err)
	}
	txID, err := tronCli.BroadcastTransaction(unsignedTx)
	if err != nil {
		return fmt.Errorf("broadcast approve: %w", err)
	}
	fmt.Printf("Approve submitted! TXID: %s\n", txID)
	fmt.Printf("View: https://tronscan.org/#/transaction/%s\n", txID)
	return nil
}

// BuildApproveCalldata builds calldata for token.approve(spender, amount).
func BuildApproveCalldata(spender common.Address, amount *big.Int) []byte {
	selector := []byte{0x09, 0x5e, 0xa7, 0xb3} // keccak256("approve(address,uint256)")[:4]
	out := make([]byte, 4+64)
	copy(out[0:4], selector)
	copy(out[4:36], leftPad(spender.Bytes(), 32))
	copy(out[36:68], leftPad(amount.Bytes(), 32))
	return out
}

// SwapTokenForTRX swaps USDJ -> TRX via the Universal Router.
// Requires prior approve() so the router can pull tokens.
// Commands: V3_SWAP_EXACT_IN (sell USDJ -> WTRX) + UNWRAP_WETH (unwrap -> TRX)
func SwapTokenForTRX(
	tronCli *TronClient,
	privateKeyHex string,
	ownerBase58 string,
	tokenAmountRaw *big.Int,
	slippageBips uint64,
	feeLimitSun int64,
	dryRun bool,
) error {
	ownerEth := mustDecodeAddress(ownerBase58)

	// ------- Step 1: Get quote -------
	quoteURL := BuildQuoteURL(TRON_USDT, TRON_WTRX, tokenAmountRaw)
	fmt.Printf("\nFetching quote: %s\n", quoteURL)

	// Output is WTRX (6 decimals)
	quote, err := GetQuote(quoteURL, DECIMALS_TRX)
	if err != nil {
		return fmt.Errorf("get quote: %w", err)
	}

	fmt.Printf("Quote: %s Token -> %s TRX (fee: %s%%)\n",
		toDecimal(tokenAmountRaw, DECIMALS_USDJ), quote.AmountOut, quote.Fee)

	amountOutMin := new(big.Int).Mul(quote.AmountOutRaw, new(big.Int).SetUint64(10000-slippageBips))
	amountOutMin.Div(amountOutMin, big.NewInt(10000))
	fmt.Printf("Min out (%.2f%% slippage): %s TRX\n",
		float64(slippageBips)/100.0, toDecimal(amountOutMin, DECIMALS_TRX))

	// ------- Step 2: Build commands -------
	sellAddr := mustDecodeAddress(TRON_USDT)
	buyAddr := mustDecodeAddress(TRON_WTRX)
	routerAddr := mustDecodeAddress(UNIVERSAL_ROUTER)

	// V3_SWAP_EXACT_IN (0x00): sell USDJ -> WTRX
	// recipient = router (WTRX stays in router for unwrap)
	// payerIsUser=true: router pulls USDJ from user (needs approve)
	v3Input := EncodeV3SwapExactIn(
		routerAddr,
		sellAddr,
		buyAddr,
		v3FeeFromPoolFee(quote.PoolFee),
		tokenAmountRaw,
		amountOutMin,
		true,
	)

	// UNWRAP_WETH (0x0c): unwrap WTRX -> TRX, send to user
	unwrapInput := EncodeWrapETH(ownerEth, amountOutMin)

	commands := []byte{CmdV3SwapExactIn, CmdUnwrapWETH}
	inputs := [][]byte{v3Input, unwrapInput}
	deadline := uint64(time.Now().Unix() + 600)
	calldata := BuildExecuteCalldata(commands, inputs, deadline)

	// ------- Step 3: Build tx -------
	ownerHex := Base58ToHex(ownerBase58)
	routerHex := Base58ToHex(UNIVERSAL_ROUTER)

	unsignedTx, err := tronCli.TriggerSmartContract(TriggerRequest{
		OwnerAddress:    ownerHex,
		ContractAddress: routerHex,
		Data:            fmt.Sprintf("%x", calldata),
		CallValue:       0, // no TRX sent
		FeeLimit:        feeLimitSun,
		Visible:         false,
	})
	if err != nil {
		return fmt.Errorf("build tx: %w", err)
	}
	fmt.Printf("Unsigned TXID: %s\n", unsignedTx.TxID)

	fmt.Println("\nNOTE: Router must be approved to spend your tokens.")
	fmt.Printf("  Run: swap-tron approve -key <key> -owner %s -amount <amount>\n\n", ownerBase58)

	if dryRun {
		fmt.Println("Dry run: done (not signed / broadcast)")
		return nil
	}

	// ------- Step 4: Sign -------
	if err := SignTransaction(unsignedTx, privateKeyHex); err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	fmt.Printf("Signature: %s...\n", unsignedTx.Signature[0][:40])

	// ------- Step 5: Broadcast -------
	txID, err := tronCli.BroadcastTransaction(unsignedTx)
	if err != nil {
		return fmt.Errorf("broadcast: %w", err)
	}
	fmt.Printf("Swap submitted! TXID: %s\n", txID)
	fmt.Printf("View: https://tronscan.org/#/transaction/%s\n", txID)
	return nil
}

// ---------------------------------------------------------------------------
// Fee helper
// ---------------------------------------------------------------------------

func v3FeeFromPoolFee(poolFee string) uint32 {
	switch poolFee {
	case "100":
		return 100
	case "500":
		return 500
	case "3000":
		return 3000
	case "10000":
		return 10000
	default:
		return 3000
	}
}

// ---------------------------------------------------------------------------
// Decimal helpers
// ---------------------------------------------------------------------------

func toDecimal(val *big.Int, decimals uint) string {
	if val == nil {
		return "0"
	}
	div := new(big.Int).Exp(big.NewInt(10), new(big.Int).SetUint64(uint64(decimals)), nil)
	intPart := new(big.Int).Div(val, div)
	fracPart := new(big.Int).Sub(val, new(big.Int).Mul(intPart, div))
	fracStr := fmt.Sprintf("%0*d", decimals, fracPart)
	fracStr = strings.TrimRight(fracStr, "0")
	if fracStr == "" {
		fracStr = "0"
	}
	return fmt.Sprintf("%s.%s", intPart.String(), fracStr)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
