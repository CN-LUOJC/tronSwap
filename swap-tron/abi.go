package main

import (
	"encoding/binary"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// ---------------------------------------------------------------------------
// Manual ABI encoding for SUN.io Universal Router.execute()
// function execute(bytes commands, bytes[] inputs, uint256 deadline)
// ---------------------------------------------------------------------------

var executeSelector = crypto.Keccak256([]byte("execute(bytes,bytes[],uint256)"))[:4]

// BuildExecuteCalldata builds the full calldata for UniversalRouter.execute()
// with proper ABI encoding for the nested dynamic types.
func BuildExecuteCalldata(commands []byte, inputs [][]byte, deadline uint64) []byte {
	commandsLen := len(commands)
	commandsPadded := padTo32(commands)
	commandsPaddedLen := len(commandsPadded)

	// Calculate inputs array metadata
	var inputsOffsets []int
	currentInputOffset := 32 + 32*len(inputs)
	for _, in := range inputs {
		inputsOffsets = append(inputsOffsets, currentInputOffset)
		padded := padTo32(in)
		currentInputOffset += 32 + len(padded)
	}
	inputsTotalSize := currentInputOffset

	// Offsets for the top-level tuple (relative to position 0 = after selector)
	staticFieldsSize := 4 + 32*3
	commandsDataOffset := staticFieldsSize
	inputsDataOffset := commandsDataOffset + 32 + commandsPaddedLen

	totalSize := inputsDataOffset + inputsTotalSize
	buf := make([]byte, totalSize)

	copy(buf[0:4], executeSelector)

	// Tuple offsets are relative to position 4 (start of tuple encoding)
	putUint64(buf[4:36], uint64(commandsDataOffset-4))
	putUint64(buf[36:68], uint64(inputsDataOffset-4))
	putUint64(buf[68:100], deadline)

	// Commands section
	cmdPos := commandsDataOffset
	putUint64(buf[cmdPos:cmdPos+32], uint64(commandsLen))
	copy(buf[cmdPos+32:cmdPos+32+commandsPaddedLen], commands)

	// Inputs section
	inPos := inputsDataOffset
	putUint64(buf[inPos:inPos+32], uint64(len(inputs)))

	for i, offset := range inputsOffsets {
		putUint64(buf[inPos+32+i*32:inPos+64+i*32], uint64(offset))
	}

	for i, in := range inputs {
		elemPos := inPos + inputsOffsets[i]
		padded := padTo32(in)
		putUint64(buf[elemPos:elemPos+32], uint64(len(in)))
		copy(buf[elemPos+32:elemPos+32+len(padded)], in)
	}

	return buf
}

func padTo32(b []byte) []byte {
	rem := len(b) % 32
	if rem == 0 {
		return b
	}
	padded := make([]byte, len(b)+32-rem)
	copy(padded, b)
	return padded
}

func putUint64(buf []byte, val uint64) {
	for i := 0; i < 24; i++ {
		buf[i] = 0
	}
	binary.BigEndian.PutUint64(buf[24:32], val)
}

// ---------------------------------------------------------------------------
// Command IDs
// ---------------------------------------------------------------------------

const (
	CmdV3SwapExactIn  = byte(0x00)
	CmdV3SwapExactOut = byte(0x01)
	CmdWrapETH        = byte(0x0b)
	CmdUnwrapWETH     = byte(0x0c)
)

// ---------------------------------------------------------------------------
// EncodeWrapETH: abi.encode(address recipient, uint256 amountMin)
// ---------------------------------------------------------------------------

func EncodeWrapETH(recipient common.Address, amountMin *big.Int) []byte {
	out := make([]byte, 64)
	copy(out[0:32], leftPad(recipient.Bytes(), 32))
	copy(out[32:64], leftPad(amountMin.Bytes(), 32))
	return out
}

// ---------------------------------------------------------------------------
// EncodeV3SwapExactIn:
//   abi.encode(address recipient, uint256 amountIn, uint256 amountOutMin,
//              bytes path, bool payerIsUser)
// ---------------------------------------------------------------------------

func EncodeV3SwapExactIn(recipient, tokenIn, tokenOut common.Address, fee uint32, amountIn, amountOutMin *big.Int, payerIsUser bool) []byte {
	path := encodePacked(tokenIn, fee, tokenOut)

	staticSize := 32 * 5
	pathPadded := padTo32(path)
	totalSize := staticSize + 32 + len(pathPadded)

	out := make([]byte, totalSize)

	copy(out[0:32], leftPad(recipient.Bytes(), 32))
	copy(out[32:64], leftPad(amountIn.Bytes(), 32))
	copy(out[64:96], leftPad(amountOutMin.Bytes(), 32))

	// Offset to path (relative to start of struct encoding = staticSize)
	putUint64(out[96:128], uint64(staticSize))

	var boolVal [32]byte
	if payerIsUser {
		boolVal[31] = 1
	}
	copy(out[128:160], boolVal[:])

	// Dynamic path data
	putUint64(out[160:192], uint64(len(path)))
	copy(out[192:192+len(pathPadded)], path)

	return out
}

// ---------------------------------------------------------------------------
// encodePacked(address, uint24, address) — for V3 swap paths
// ---------------------------------------------------------------------------

func encodePacked(addr1 common.Address, fee uint32, addr2 common.Address) []byte {
	out := make([]byte, 43)
	copy(out[0:20], addr1.Bytes())
	out[20] = byte(fee >> 16)
	out[21] = byte(fee >> 8)
	out[22] = byte(fee)
	copy(out[23:43], addr2.Bytes())
	return out
}

// ---------------------------------------------------------------------------
// Helper: left-pad bytes to target length
// ---------------------------------------------------------------------------

func leftPad(b []byte, length int) []byte {
	if len(b) >= length {
		return b[:length]
	}
	out := make([]byte, length)
	copy(out[length-len(b):], b)
	return out
}
