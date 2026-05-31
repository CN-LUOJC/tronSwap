package main

import (
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
)

const (
	DEFAULT_TRON_API      = "https://api.trongrid.io"
	DEFAULT_FEE_LIMIT     = 150_000_000
	DEFAULT_SLIPPAGE_BIPS = 50
	HELP_TEXT = `TRX Flash Swap on TRON — SUN.io Universal Router

Commands:
  swap-tron trx2token -key <key> -owner <addr> -amount <TRX>   TRX → Token/USDT
  swap-tron token2trx -key <key> -owner <addr> -amount <token>  Token/USDT → TRX
  swap-tron approve   -key <key> -owner <addr> -amount <token>  Approve router to spend tokens
  swap-tron trx2token -owner <addr> -amount 10 -dry             Dry-run (no sign/broadcast)

Global flags:
  -key         Private key (hex)
  -owner       Wallet address (base58)
  -amount      Amount to swap
  -dry         Dry run only (no sign/broadcast)
  -slippage    Slippage in bips  (default 50 = 0.5%%)
  -fee-limit   Fee limit in SUN  (default 150000000)
  -api         TRON API endpoint (default https://api.trongrid.io)

Examples:
  swap-tron trx2token -key abc123... -owner TVtwXQg... -amount 100
  swap-tron trx2token -owner TVtwXQg... -amount 50 -dry
  swap-tron approve -key abc123... -owner TVtwXQg... -amount 1000
  swap-tron token2trx -key abc123... -owner TVtwXQg... -amount 500
`
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, HELP_TEXT)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "trx2token":
		runTRX2Token(os.Args[2:])
	case "token2trx":
		runToken2TRX(os.Args[2:])
	case "approve":
		runApprove(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, HELP_TEXT)
		os.Exit(1)
	}
}

func parseFlags(args []string) (privateKey, owner string, amount float64, dryRun bool, slippage uint64, feeLimit int64, apiURL string) {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	fs.StringVar(&privateKey, "key", "", "")
	fs.StringVar(&owner, "owner", "", "")
	fs.Float64Var(&amount, "amount", 0, "")
	fs.BoolVar(&dryRun, "dry", false, "")
	fs.Uint64Var(&slippage, "slippage", DEFAULT_SLIPPAGE_BIPS, "")
	fs.Int64Var(&feeLimit, "fee-limit", DEFAULT_FEE_LIMIT, "")
	fs.StringVar(&apiURL, "api", DEFAULT_TRON_API, "")
	fs.Parse(args)
	return
}

func amountToSun(amount float64) *big.Int {
	sun := new(big.Int)
	new(big.Float).Mul(big.NewFloat(amount), big.NewFloat(1_000_000)).Int(sun)
	return sun
}

func amountToRaw(amount float64, decimals uint) *big.Int {
	raw := new(big.Int)
	multiplier := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	new(big.Float).Mul(big.NewFloat(amount), multiplier).Int(raw)
	return raw
}

func mustOwner(owner string) {
	if owner == "" {
		fmt.Fprintln(os.Stderr, "Error: -owner is required")
		os.Exit(1)
	}
}

func mustKey(key string, dry bool) {
	if key == "" && !dry {
		fmt.Fprintln(os.Stderr, "Error: -key is required (use -dry for dry run)")
		os.Exit(1)
	}
}

func mustAmount(amount float64) {
	if amount <= 0 {
		fmt.Fprintln(os.Stderr, "Error: -amount must be > 0")
		os.Exit(1)
	}
}

// ---- trx2token ----

func runTRX2Token(args []string) {
	key, owner, amount, dry, slip, fee, api := parseFlags(args)
	mustOwner(owner)
	mustAmount(amount)
	mustKey(key, dry)

	amountSun := amountToSun(amount)
	printHeader("TRX -> TOKEN", amount, amountSun, owner, slip, fee, dry, key)

	tronCli := NewTronClient(api)
	err := SwapTRXForToken(tronCli, key, owner, amountSun, slip, fee, dry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nDone.")
}

// ---- token2trx ----

func runToken2TRX(args []string) {
	key, owner, amount, dry, slip, fee, api := parseFlags(args)
	mustOwner(owner)
	mustAmount(amount)
	mustKey(key, dry)

	amountRaw := amountToRaw(amount, DECIMALS_USDJ)
	printHeader("TOKEN -> TRX", amount, amountRaw, owner, slip, fee, dry, key)

	tronCli := NewTronClient(api)
	err := SwapTokenForTRX(tronCli, key, owner, amountRaw, slip, fee, dry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nDone.")
}

// ---- approve ----

func runApprove(args []string) {
	key, owner, amount, dry, _, fee, api := parseFlags(args)
	mustOwner(owner)
	mustAmount(amount)
	mustKey(key, dry)

	amountRaw := amountToRaw(amount, DECIMALS_USDJ)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("  Approve Router to spend tokens")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("  Token:      %s\n", TRON_USDT)
	fmt.Printf("  Router:     %s\n", UNIVERSAL_ROUTER)
	fmt.Printf("  Owner:      %s\n", owner)
	fmt.Printf("  Amount:     %s\n", toDecimal(amountRaw, DECIMALS_USDJ))
	fmt.Printf("  Dry Run:    %v\n", dry)
	fmt.Println(strings.Repeat("=", 60))

	tronCli := NewTronClient(api)
	err := ApproveTokenForRouter(tronCli, key, owner, TRON_USDT, amountRaw, fee, dry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nDone.")
}

// ---- shared header ----

func printHeader(label string, amount float64, amountRaw *big.Int, owner string, slip uint64, fee int64, dry bool, key string) {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("  %s\n", label)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("  Amount:     %.4f (%s raw)\n", amount, amountRaw.String())
	fmt.Printf("  WTRX:       %s\n", TRON_WTRX)
	fmt.Printf("  USDT:       %s\n", TRON_USDT)
	fmt.Printf("  Router:     %s\n", UNIVERSAL_ROUTER)
	fmt.Printf("  Slippage:   %.2f%%\n", float64(slip)/100.0)
	fmt.Printf("  Fee Limit:  %d SUN\n", fee)
	fmt.Printf("  Owner:      %s\n", owner)
	fmt.Printf("  Dry Run:    %v\n", dry)
	if !dry && key != "" {
		fmt.Printf("  PrivateKey: %s...%s\n", key[:8], key[len(key)-4:])
	}
	fmt.Println(strings.Repeat("=", 60))
}
