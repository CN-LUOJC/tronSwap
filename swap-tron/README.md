# TRX -> USDT Flash Swap (TRON)

通过 SUN.io Universal Router 在 TRON 链上进行 TRX 兑换 USDT 的 Go 实现。

## 合约地址

| 合约 | 地址 | 说明 |
|------|------|------|
| **Router（路由器）** | `TQqgNg13s2DjvXhW1ky4v6TsR8wZGvb7Y4` | Universal Router，支持高达99%能量费补贴 |
| **WTRX** | `TNUC9Qb1rRpS5CbWLmNMxXBjyFoydXjWFR` | 包装TRX (6位小数) |
| **USDT** | `TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t` | 官方TRC20 USDT (6位小数) |

### 关于 Router 地址

`TQqgNg13s2DjvXhW1ky4v6TsR8wZGvb7Y4` 是一个能量补贴版 Universal Router 合约，可提供最高 99% 的能量费减免，大幅降低交易手续费。

---

## 环境准备

### 1. 安装 Go

确保已安装 Go 1.21+。验证：

```bash
go version
```

### 2. 配置钱包（私钥）

你需要一个 TRON 钱包的私钥。私钥是 64 位十六进制字符串（或带 `0x` 前缀）。

> **安全警告**：私钥等同于资产的完全控制权。请确保：
> - 仅在本地、可信的环境使用
> - 不提交到 Git 仓库
> - 测试用钱包不要存放大额资产

#### 获取私钥的方法

**TronLink 浏览器插件钱包：**
1. 打开 TronLink -> 设置 -> 导出私钥
2. 复制私钥（Hex格式，64位）

**从助记词推导（使用 tronweb）：**
```javascript
const TronWeb = require('tronweb');
const tronWeb = new TronWeb({fullHost: 'https://api.trongrid.io'});
const account = await tronWeb.fromMnemonic('your mnemonic phrase here', 'm/44\'/195\'/0\'/0/0');
console.log('Private Key:', account.privateKey);
console.log('Address:', account.address);
```

### 3. 获取 TRX 作为 Gas

主网上需要 TRX 作为能量费。可以通过交易所购买并提现到钱包地址。

---

## 编译与运行

```bash
cd swap-tron

# 下载依赖
go mod tidy

# 编译
go build -o swap-tron.exe .

# Windows 直接双击 swap-tron.exe，或用命令行运行
```

---

## 使用说明

### 完整兑换

```bash
swap-tron -key <私钥> -owner <钱包地址> -amount <TRX数量>
```

示例：

```bash
swap-tron -key abc123...def -owner TVtwXQg4dRii7BBXAojGMb1sqiiyt4zg9p -amount 100
```

### 仅询价（Dry Run，推荐先测试）

不加 `-key`，只获取报价和构建交易，不签名不广播：

```bash
swap-tron -owner TVtwXQg4dRii7BBXAojGMb1sqiiyt4zg9p -amount 50 -dry
```

输出示例：
```
============================================================
  TRX -> USDT Flash Swap via SUN.io Universal Router
============================================================
  Amount:     50.0000 TRX (50000000 SUN)
  ...
  Dry Run:    true
============================================================

Fetching quote: https://xxxx/swap/router?fromToken=...
Quote: 50.0 TRX -> 18.571234 USDT (fee: 0.005000%)
Min out (0.50% slippage): 18.478377 USDT
Unsigned TXID: f9f902bf... (交易已构建，未签名)
Dry run: skipping sign and broadcast
```

### 自定义滑点

滑点默认 0.5%（50 bips），可按需调整：

```bash
swap-tron -key <key> -owner <addr> -amount 100 -slippage 100   # 1.0%
swap-tron -key <key> -owner <addr> -amount 100 -slippage 200   # 2.0%
swap-tron -key <key> -owner <addr> -amount 100 -slippage 10    # 0.1%
```

### 自定义交易费上限

```bash
swap-tron -key <key> -owner <addr> -amount 100 -fee-limit 300000000
```

### 使用其他 TRON 节点

```bash
swap-tron -key <key> -owner <addr> -amount 100 -api https://api.trongrid.io
```

---

## 完整参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-key` | 私钥(64位hex) | - |
| `-owner` | 钱包地址(base58) | - |
| `-amount` | TRX数量 | - |
| `-dry` | 仅询价(不签名) | false |
| `-slippage` | 滑点(bips) | 50 (0.5%) |
| `-fee-limit` | 能量费上限(SUN) | 150000000 (150 TRX) |
| `-api` | TRON节点API | https://api.trongrid.io |

---

## 兑换流程

```
TRX -> [WRAP_ETH] -> WTRX -> [V3_SWAP_EXACT_IN] -> USDT
                           (SunSwap V3 Pool)
```

执行细节：
1. 调用 SunSwap 询价 API 获取最优路径和预估输出
2. 构建 Universal Router 指令序列：`WRAP_ETH` + `V3_SWAP_EXACT_IN`
3. 使用 `TriggerSmartContract` API 构建未签名交易
4. 使用 ECDSA secp256k1 对交易签名
5. 广播交易到 TRON 主网

---

## 测试网

SunSwap V3 **未部署**在 Shasta/Nile 测试网上，因此无法在测试网上进行完整测试。建议：

1. **先使用 `-dry` 模式验证流程**（可拿到报价、构建交易）
2. **主网上先小额测试**（如 1-5 TRX）
3. **确认无误后再进行大额兑换**

---

## 故障排查

### "insufficient energy"

交易能量费不够。增加 `-fee-limit`：
```bash
swap-tron -key <key> -owner <addr> -amount 10 -fee-limit 300000000
```

### 交易广播失败

- 确认钱包有足够 TRX 余额（用于能量费和兑换本金）
- 确认私钥正确
- 确认合约地址未过期

### 报价API超时

- 确认网络能访问 `rot.endjgfsv.link`
- 可更换 TRON API 节点：`-api https://api.tronstack.io`
