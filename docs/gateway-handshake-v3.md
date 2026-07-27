# Gateway 握手 v3：transcript 完整性与前向保密

> 状态：已实现（2026-07-27）。§1–§5 描述当前 `modules/gateway-core` 的实际行为；§6 的实施步骤和 §10 的验收清单保留为落地记录。
>
> 本文针对 v2 四步握手中已实证的重放缺陷给出修复方案，并顺带关闭前向保密与 label 枚举两个已知弱点。修复范围完全落在 `modules/gateway-core`，不改变 Face/Hand 业务协议、不改变凭据格式、不要求重新签发 token。
>
> 落地偏差：§5.7 原计划用进程级 HMAC 派生确定性 decoy 凭据，实现改为每次连接生成一次性随机秘密 —— 同样使未知 label 走完整握手并在 proof 处统一失败，但更简单且无需持有额外进程密钥。`PrincipalID` 设为 `"decoy"` 而非留空，由独立的 `authErr` 判定保证该路径绝不产生可用 peer。`pendingConns` 上限未实现，与握手限流一并留待 §9.3。

相关文档：

- [`face-protocol.md`](face-protocol.md)：Face identity、scope 与事件投影，本文不改动其语义；
- [`ai-face-protocol.md`](ai-face-protocol.md)：客户端接入指南，握手版本号变更后需同步更新；
- [`archive/remote-execution-closed-loop.md`](archive/remote-execution-closed-loop.md)：RemoteRun 与 Hand 最终守门，是本缺陷影响面的评估依据。

---

## 1. 摘要

v2 握手的密码学原语选择和使用是正确的：HKDF 做了用途与方向隔离，GCM 的 AAD 绑定了完整 Envelope 头，JSON 严格解码，seq 严格单调，凭据比较常量时间，长期秘密不出现在任何明文帧中。

问题不在原语，在于**握手 transcript 不完整**。`protocol.HandshakeTranscript` 只覆盖了前两帧字段的一个子集，被排除在外的 `RegisterChallenge.ExpiresAt` 既不参与密钥派生也不被任何 AEAD 认证，而它恰好是客户端唯一的新鲜性锚点。加上客户端在整个握手中不贡献任何熵，结果是：

**攻击者无需任何秘密，即可把一段录制的 server→client 流重放给 Hand，使其完成握手并重新执行历史上已被批准的命令。**

该攻击已用 PoC 在 `-race` 下跑通（见 §3.1）。

本文提出的 v3 握手用一次性切换同时修掉三件事：

1. transcript 覆盖前两帧的全部字段，并用结构性测试锁死"新增字段必须进 transcript"这条不变量；
2. 引入 X25519 ephemeral ECDH，与现有 PSK 共同派生会话密钥 —— 客户端因此贡献熵（关闭重放），密钥不再可由长期秘密重算（获得前向保密）；
3. 认证查询后移到 proof 之后，未知 label 与错误秘密的失败路径不可区分（关闭枚举 oracle）。

凭据格式不变，`hand_credentials` / `face_tokens` 无需迁移，不需要重新签发任何 token。

---

## 2. 威胁模型

### 2.1 当前部署形态

- Mind 的 `http.Server`（`cmd/half-pi-mind/hub_server.go:64`）不启用 TLS，默认监听 `15707`；
- Hand 默认连接 `ws://127.0.0.1:15707/ws`，但 Hand 的存在意义就是跨网执行，实际部署会走公网或内网；
- 应用层 AES-128-GCM 是当前唯一的机密性与完整性来源。

### 2.2 假定的攻击者能力

| 能力 | 是否假定具备 | 说明 |
|---|---|---|
| 被动录制流量 | 是 | 无 TLS，链路上任意位置可录 |
| 主动上路（改写、注入、丢弃帧） | 是 | ARP/DNS 欺骗、恶意中间设备、端口抢占 |
| 读取 Mind SQLite 或 Hand `config.toml` | 否（作为独立场景讨论） | 文件已是 `0600` + 目录 `0700` + Windows DACL |
| 破解 AES-128-GCM / X25519 | 否 | |

### 2.3 应当保持的不变量

- 未持有 `token` 与 `application_key` 双秘密者，不能建立会话、不能解密任何业务 payload、不能让任一端接受任何业务消息；
- 双秘密事后泄露，不能解密此前录制的流量（v2 不满足，本文修复）；
- Hand 侧 Authorizer 与 Mind 侧 ToolRuntime 的守门不被握手层绕过；
- 单向流内不存在重放、乱序或静默丢弃。

---

## 3. 缺陷清单

### 3.1 严重 — 录制的 server→client 流可被无秘密重放（CWE-294）

**根因**：`protocol.HandshakeTranscript`（`protocol/protocol.go:139-147`）包含 `protocol_version / peer_type / label / handshake_id / server_id / session_id / challenge`，**不包含** `RegisterChallenge.ExpiresAt` 与 `Algorithm`；`RegisterProofAAD`（同文件 `150-158`）同样不包含。因此 `ExpiresAt` 是一个攻击者可任意改写、且改写后不会导致任何后续验证失败的自证字段。

而客户端唯一的新鲜性检查正是基于它（`wss/client.go:138`）：

```go
challenge.ExpiresAt <= time.Now().UnixMilli() ||
challenge.ExpiresAt > deadline.Add(time.Second).UnixMilli()
```

客户端在整个握手中不贡献任何熵，也不记忆用过的 `handshake_id`。会话密钥完全由 `(token, application_key, transcript)` 决定，而 transcript 完全由服务端选择。攻击者复现 transcript 即可让客户端复现密钥。

**攻击链**（已实证）：

1. 被动录制一次完整会话，得到 `register_challenge`、`registered` 与后续 `rpc` 帧；
2. 上路等待 Hand 重连 —— Hand 断线自动重连，退避从 1s 起（`hand.retry.max_backoff` 默认 60s），机会反复出现；
3. 重放录制的 `register_challenge`，**仅**把 `expires_at` 改写为 `now+5s`；
4. 客户端派生出与录制会话**完全相同**的三把密钥，发出 proof（攻击者直接丢弃，无需也无法验证）；
5. 重放录制的 `registered` 帧 —— `Session.Accept` 通过（`session_id`/`from`/`to`/`seq` 全部一致），S→C 密钥解密通过，字段比对通过，**握手完成**；
6. 按序重放录制的 `rpc` 帧 —— Hand 解密通过、seq 校验通过，交给 `onMessage`。

PoC 实测输出：

```
原始 expires_at = 1785059662921 (已过期 -9.896679074s)
!! 重放握手成功：客户端接受了录制的 registered 帧
!! 重放业务消息成功解密: type=rpc payload={"args":{"command":"echo pwned"},"tool":"exec_command"}
```

**影响**：对一个远程执行系统，这意味着历史上被批准过的命令可被重新触发。Hand 侧 Authorizer 拦不住 —— 那条命令当初就是放行的；Mind 侧审批链路根本没有参与，因为 Mind 不在这条连接上。

**方向性**：漏洞只打客户端。服务端每次握手生成新的 `handshake_id`/`session_id`/`challenge`，录制的 proof 无法通过验证，因此无法反向重放到 Mind。

### 3.2 中等 — 无前向保密（CWE-522）

会话密钥 = `HKDF(ikm = token ‖ application_key, salt = challenge, info = purpose ‖ H(transcript))`。纯 PSK，无 DH 成分。

任何时刻取得双秘密者，可解密**全部历史录制流量**，并可双向冒充（对称 PSK，持钥方同时能扮演客户端与服务端）。

秘密的存放位置本身已有合理保护（Mind SQLite 与 Hand `config.toml` 均为 `0600`，目录 `0700`，Windows 侧有 DACL），但它们是**可还原的明文** —— PSK 设计的必然结果，服务端必须能取回原值才能派生密钥，无法像口令那样只存哈希。因此 root 权限、备份泄露、磁盘取证、误提交都直接等价于历史流量全解密。

### 3.3 低 — label 枚举 oracle

`hub.ServeWS` 在 `authenticateRegister`（`hub/hub.go:117`）**先于**发出 challenge：label 不存在立即返回 `authentication_failed`，label 存在则收到 challenge。攻击者据此可枚举全部已注册的 Hand/Face label。没有失败速率限制，也没有锁定。

### 3.4 低 — 部署面与可读性

- `wss.NewServer` 使用零值 `websocket.Upgrader{}`（`wss/server.go:18`），无显式 Origin 策略。gorilla 的默认行为在无 `Origin` 头时放行，对非浏览器客户端正确，但缺少显式声明；
- 握手与业务帧的元数据（peer type、label、时序、长度）始终明文可见。应用层加密替代了 TLS 的机密性，没有替代它的元数据保护；
- `Envelope.AAD()` 的 `v` 字段硬编码为 `1`，与 `ProtocolVersion = 2` 语义不同但形似，易混淆。

---

## 4. 为什么"最小补丁"不够

直觉上的最小修复是：把 `ExpiresAt` 和 `Algorithm` 加进 `HandshakeTranscript`，不动 wire 格式。这确实让改写 `expires_at` 导致密钥不匹配，把重放窗口从**无限**压回**服务端设定的 10 秒**。

但 10 秒窗口在本项目的部署形态下仍然可利用：

> 攻击者在 `t` 时刻录制 challenge C（`expires_at = t+10s`），随即切断连接。Hand 在 `t+1s` 按退避重连。攻击者原样重放 C：客户端检查 `expires_at > now`（`t+10 > t+1` ✓）且 `expires_at <= now+11s`（`t+10 <= t+12` ✓），**通过**。密钥仍然复现，攻击链 §3.1 的第 4-6 步照常成立。

Hand 的自动重连（退避下界 1s）恰好把这个窗口变成攻击者可主动制造的条件，而不是需要等待的巧合。

因此**客户端必须贡献熵**。既然无论如何都要改 wire、都要 bump 协议版本、都要一次全量切换，就没有理由分两次做 —— 让客户端贡献的那 32 字节直接是 X25519 公钥，同一次切换顺带拿到前向保密。这就是 §5 的设计。

如果因为紧急程度必须先出一个不改 wire 的版本，`ExpiresAt` + `Algorithm` 入 transcript 是可以单独先行的（§7.3），但必须在发布说明中写明**残留 10 秒窗口**，不能宣称问题已解决。

---

## 5. v3 握手设计

### 5.1 总体

保持现有四步结构与消息类型，只在前两帧各加一个字段：

```
Client                                                     Mind
  |  register        { ver=3, client_id, type, client_share } |   明文
  |---------------------------------------------------------->|
  |  register_challenge { ..., expires_at, alg, server_share } |   明文
  |<----------------------------------------------------------|
  |  register_proof  { ver=3, handshake_id, alg, proof }       |   Proof 密钥加密
  |---------------------------------------------------------->|
  |  registered      { EncryptedPayload }                      |   S→C 密钥加密
  |<----------------------------------------------------------|
```

`client_share` / `server_share` 是标准 base64 编码的 32 字节 X25519 公钥。两端在收到对方公钥后计算 ECDH 共享秘密，与 PSK 一起进入 HKDF。

关键性质：

- 客户端的 ephemeral 私钥每次连接新生成，攻击者无法让客户端复现旧密钥 → **重放关闭**；
- ephemeral 私钥用后即弃，事后取得 PSK 也无法重算历史会话密钥 → **前向保密**；
- PSK 仍然是唯一的认证材料，未持有双秘密者的 ECDH 结果毫无用处 → **认证强度不变**。

即 TLS-PSK-ECDHE 的最小形态。

### 5.2 wire 变更

```go
// protocol/protocol.go

const ProtocolVersion = 3   // 由 2 提升；v2 与 v3 互相 fail closed

// Register 是 Face 或 Hand 首次连接时发送的公开路由信息，不包含长期秘密。
type Register struct {
	ProtocolVersion int      `json:"protocol_version"`
	ClientID        string   `json:"client_id"`
	Type            PeerType `json:"type"`
	ClientShare     string   `json:"client_share"` // 标准 base64 的 32 字节 X25519 公钥
}

// RegisterChallenge 是服务端绑定当前连接发出的单次挑战。
type RegisterChallenge struct {
	ProtocolVersion int    `json:"protocol_version"`
	HandshakeID     string `json:"handshake_id"`
	ServerID        string `json:"server_id"`
	SessionID       string `json:"session_id"`
	Challenge       string `json:"challenge"`
	ExpiresAt       int64  `json:"expires_at"`
	Algorithm       string `json:"algorithm"`
	ServerShare     string `json:"server_share"` // 标准 base64 的 32 字节 X25519 公钥
}
```

### 5.3 transcript 规范化

**不变量：`HandshakeTranscript` 必须是 `Register` 与 `RegisterChallenge` 全部字段的并集。** 这条不变量由 §8 的反射测试强制。

```go
// HandshakeTranscript 是会话密钥派生使用的规范 transcript，
// 覆盖 register 与 register_challenge 两帧的全部字段。
// 新增任何握手帧字段时必须同步加入本结构，由 TestTranscriptCoversHandshakeFrames 强制。
type HandshakeTranscript struct {
	ProtocolVersion int      `json:"protocol_version"`
	PeerType        PeerType `json:"peer_type"`
	Label           string   `json:"label"`
	HandshakeID     string   `json:"handshake_id"`
	ServerID        string   `json:"server_id"`
	SessionID       string   `json:"session_id"`
	Challenge       string   `json:"challenge"`
	ExpiresAt       int64    `json:"expires_at"`
	Algorithm       string   `json:"algorithm"`
	ClientShare     string   `json:"client_share"`
	ServerShare     string   `json:"server_share"`
}
```

`RegisterProofAAD` 不变。transcript hash 已经通过 HKDF 的 `info` 绑定了 proof 密钥，AAD 是冗余保险层，保留即可。

### 5.4 密钥派生

```go
// wss/handshake.go

// DeriveSessionKeys 按 v3 transcript、ECDH 共享秘密和两项长期秘密
// 派生 proof、C→S 和 S→C 密钥。
// shared 必须是 32 字节 X25519 输出；调用方在派生完成后应立即清零。
func DeriveSessionKeys(token, applicationKey string, shared []byte, transcript protocol.HandshakeTranscript) (SessionKeys, error) {
	var keys SessionKeys
	if len(shared) != sharedSecretSize {   // 32
		return keys, fmt.Errorf("shared secret must be %d bytes", sharedSecretSize)
	}
	tokenBytes, err := decodeHandshakeSecret("token", token)
	if err != nil {
		return keys, err
	}
	applicationKeyBytes, err := decodeHandshakeSecret("application key", applicationKey)
	if err != nil {
		return keys, err
	}
	// 三段均为定长（16 + 16 + 32），拼接无歧义。
	root := make([]byte, 0, len(tokenBytes)+len(applicationKeyBytes)+len(shared))
	root = append(root, tokenBytes...)
	root = append(root, applicationKeyBytes...)
	root = append(root, shared...)
	defer clear(root)

	challenge, err := decodeCanonicalBase64(transcript.Challenge, 32)
	if err != nil {
		return keys, fmt.Errorf("challenge must be canonical base64 of 32 bytes")
	}
	transcriptJSON, err := json.Marshal(transcript)
	if err != nil {
		return keys, fmt.Errorf("marshal handshake transcript: %w", err)
	}
	hash := sha256.Sum256(transcriptJSON)
	derive := func(info string) ([]byte, error) {
		return hkdf.Key(sha256.New, root, challenge, info+string(hash[:]), KeySize)
	}
	// proof / c2s / s2c 派生与错误处理保持不变
	...
}
```

`info` 前缀（`half-pi/v3/...`，以 `/` 结尾）后接定长 32 字节 hash，无歧义；三个 `info` 常量的版本段同步改为 `v3`，确保跨版本密钥域隔离。

### 5.5 ECDH 辅助

```go
// wss/handshake.go

const sharedSecretSize = 32

// NewEphemeralShare 生成一次性 X25519 密钥对，返回私钥和其标准 base64 公钥。
func NewEphemeralShare() (*ecdh.PrivateKey, string, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generate ephemeral share: %w", err)
	}
	return priv, base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()), nil
}

// ComputeSharedSecret 校验对端公钥编码并计算 X25519 共享秘密。
// 依据 crypto/ecdh 文档，X25519 的 ECDH 在结果为全零值时返回错误，
// 因此低阶点无需额外检查 —— 实施时由 TestLowOrderPeerShareRejected 固化该假设。
func ComputeSharedSecret(priv *ecdh.PrivateKey, peerShare string) ([]byte, error) {
	raw, err := decodeCanonicalBase64(peerShare, sharedSecretSize)
	if err != nil {
		return nil, fmt.Errorf("peer share must be canonical base64 of %d bytes", sharedSecretSize)
	}
	pub, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid peer share: %w", err)
	}
	shared, err := priv.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("compute shared secret: %w", err)
	}
	return shared, nil
}

// decodeCanonicalBase64 解码并要求编码规范（重新编码必须等于原串）。
func decodeCanonicalBase64(value string, size int) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != size || base64.StdEncoding.EncodeToString(raw) != value {
		return nil, fmt.Errorf("invalid canonical base64")
	}
	return raw, nil
}
```

`decodeCanonicalBase64` 同时替换 `DeriveSessionKeys` 和 `validateProofClaims` 中现有的两处 challenge 规范性内联检查，减少重复。

### 5.6 客户端流程变更（`wss/client.go`）

在 `ConnectAndRegisterContext` 中：

1. 发送 `register` 之前生成 ephemeral：

```go
ephemeral, clientShare, err := NewEphemeralShare()
if err != nil {
    return fail(err)
}
reg, err := protocol.NewEnvelope("", protocol.TypeRegister, protocol.Register{
    ProtocolVersion: protocol.ProtocolVersion,
    ClientID:        credentials.Label,
    Type:            credentials.Type,
    ClientShare:     clientShare,
})
```

2. 收到 challenge 后，现有字段校验保持不变（`expires_at` 检查仍然保留 —— 它现在是被认证的，作为服务端侧超时的一致性检查仍有意义），补充 `ServerShare` 非空；

3. 组装完整 transcript 并计算共享秘密：

```go
transcript := protocol.HandshakeTranscript{
    ProtocolVersion: protocol.ProtocolVersion,
    PeerType:        credentials.Type,
    Label:           credentials.Label,
    HandshakeID:     challenge.HandshakeID,
    ServerID:        challenge.ServerID,
    SessionID:       challenge.SessionID,
    Challenge:       challenge.Challenge,
    ExpiresAt:       challenge.ExpiresAt,
    Algorithm:       challenge.Algorithm,
    ClientShare:     clientShare,
    ServerShare:     challenge.ServerShare,
}
shared, err := ComputeSharedSecret(ephemeral, challenge.ServerShare)
if err != nil {
    return fail(err)
}
keys, err := DeriveSessionKeys(credentials.Token, credentials.ApplicationKey, shared, transcript)
clear(shared)
ephemeral = nil
if err != nil {
    return fail(err)
}
```

> `clear` 与丢弃引用只是尽力而为 —— Go 的 GC 和栈拷贝无法保证内存中不留副本。这是降低取证残留的卫生措施，不作为安全边界宣称。

### 5.7 服务端流程变更（`hub/hub.go`）

顺序调整为：**读 register → 生成 ephemeral 与 challenge 并下发 → 读 proof → 此时才做认证查询 → 派生并验证 proof**。

```go
func (h *Hub) ServeWS(conn *websocket.Conn) error {
	// ... trackPending / deadline 不变

	_, reg, err := readRegister(conn)          // 新增：校验 reg.ClientShare 规范性
	if err != nil {
		return h.failHandshake(conn, handshakeCode(err), err)
	}
	key := PeerKey{Type: reg.Type, Label: reg.ClientID}
	if err := h.bindPending(conn, key); err != nil {
		_ = conn.Close()
		return err
	}

	// 认证查询后移：未知 label 也走完整流程，避免枚举 oracle。
	ephemeral, serverShare, err := wss.NewEphemeralShare()
	if err != nil {
		_ = conn.Close()
		return err
	}
	transcript, challenge, err := h.newChallenge(key, reg.ClientShare, serverShare, deadline)
	// ... 下发 challenge（不变）

	proof, err := readProof(conn)
	if err != nil {
		return h.failHandshake(conn, handshakeCode(err), err)
	}

	authentication, err := h.authenticateRegister(key)
	if err != nil {
		// 未知 label / 无效凭据：使用确定性哑秘密走完相同的派生与验证路径，
		// 保证响应码与耗时与"秘密错误"不可区分。
		authentication = h.decoyAuthentication(key)
	}

	shared, err := wss.ComputeSharedSecret(ephemeral, reg.ClientShare)
	if err != nil {
		return h.failHandshake(conn, "authentication_failed", err)
	}
	keys, err := wss.DeriveSessionKeys(authentication.Token, authentication.ApplicationKey, shared, transcript)
	clear(shared)
	if err != nil || time.Now().After(deadline) {
		return h.failHandshake(conn, "authentication_failed", fmt.Errorf("invalid register proof"))
	}
	claims, err := wss.VerifyRegisterProof(keys, transcript, proof)
	if err != nil || authentication.PrincipalID == "" {
		return h.failHandshake(conn, "authentication_failed", fmt.Errorf("invalid register proof"))
	}
	// ... reservePeer 及之后完全不变
}
```

哑凭据由进程生命周期内的随机密钥确定性导出，使同一 label 的重复探测行为完全一致：

```go
// decoyAuthentication 为未知或无效 label 生成确定性哑凭据，
// 使认证失败路径与秘密不匹配路径在响应码和耗时上不可区分。
// PrincipalID 留空，确保这条路径永远无法产生可用 peer。
func (h *Hub) decoyAuthentication(key PeerKey) Authentication {
	mac := hmac.New(sha256.New, h.decoyKey[:])   // decoyKey 在 hub.New() 中随机生成
	mac.Write([]byte(key.Type))
	mac.Write([]byte{0})
	mac.Write([]byte(key.Label))
	sum := mac.Sum(nil)
	return Authentication{
		Token:          hex.EncodeToString(sum[:16]),
		ApplicationKey: hex.EncodeToString(sum[16:32]),
		PrincipalID:    "",
	}
}
```

`authenticateRegister` 中"authenticator 未配置"必须仍然是硬失败，不能落入 decoy 路径。

**新增的 DoS 面**：未知 label 现在也会触发一次 X25519 keygen 与一次 ECDH。X25519 单次约数十微秒，成本可接受，但 `pendingConns` 应加上限（建议 256，可配置），超限直接拒绝新连接。按来源 IP 的失败节流列为可选后续项（§9）。

---

## 6. 实施步骤

建议拆成 5 个 commit，每个独立通过 `make test`：

| # | commit | 内容 |
|---|---|---|
| 1 | `test: add gateway handshake replay regression` | 先落 §8 的重放 PoC 测试，此时**应当失败**（红），锁定缺陷 |
| 2 | `refactor: extract canonical base64 decoding in handshake` | 抽出 `decodeCanonicalBase64`，替换两处内联检查，无行为变化 |
| 3 | `feat: bind full handshake transcript and X25519 ephemeral shares` | §5.2–5.6：wire 字段、transcript、`DeriveSessionKeys` 签名、ECDH 辅助、客户端与 Hub 流程；`ProtocolVersion` 提升为 3；commit 1 的测试转绿 |
| 4 | `fix: remove handshake label enumeration oracle` | §5.7 的认证后移与 decoy 凭据，`pendingConns` 上限 |
| 5 | `docs: record gateway handshake v3 decision` | 更新 `AGENTS.md` 决策记录与 `docs/README.md` 索引，`ai-face-protocol.md` 中的版本号 |

改动范围核实（除测试外的全部调用点）：

```
protocol/protocol.go   Register / RegisterChallenge / HandshakeTranscript / ProtocolVersion
wss/handshake.go       DeriveSessionKeys 签名、info 常量、ECDH 辅助
wss/client.go          ConnectAndRegisterContext
hub/hub.go             ServeWS / newChallenge / readRegister / decoyAuthentication
```

`half-pi-mind`、`half-pi-hand`、`half-pi-face` 三个模块**无需改动** —— 它们只消费 `wss.Credentials` 与 `SessionConn`，两者签名不变。

---

## 7. 兼容性与切换

### 7.1 凭据

`token` 与 `application_key` 的格式、生成方式、存储 schema 全部不变。**不需要重新签发任何凭据**，`hand_credentials` / `face_tokens` 无需迁移。

### 7.2 版本切换

v2 与 v3 互相 fail closed，与项目在 v1→v2 时确立的"不保留降级旁路"一致：

- v2 客户端 → v3 服务端：`readRegister` 的版本检查返回 `unsupported_protocol`；
- v3 客户端 → v2 服务端：v2 服务端的 `StrictDecode` 因 `client_share` 是未知字段而拒绝，客户端收到 `authentication_failed` 或连接关闭。

`HandshakeError.Permanent()` 已经把 `unsupported_protocol` 归类为不可重试，Hand 不会陷入无效重连风暴。

**推荐切换顺序**：停止全部 Hand/Face → 升级 Mind → 升级 Hand/Face。Hand 的自动重连会在两端就位后自愈。跨版本期间连接不可用，这是有意的。

### 7.3 若必须先出紧急补丁

在无法一次性升级全部节点的情况下，可以先只做"`ExpiresAt` + `Algorithm` 入 transcript"（不改 wire，不改版本号，v2 客户端与 v2 服务端仍互通，但**新旧 v2 之间会互相拒绝**，因为密钥派生变了 —— 实际仍是一次全量切换，只是省掉了 ECDH 实现）。

选择这条路径必须在发布说明中明确写出：**残留 10 秒重放窗口，且该窗口可被攻击者通过切断连接触发 Hand 重连来主动制造**（§4）。这只是权宜，v3 仍需排期。

---

## 8. 测试矩阵

全部测试带 `-race`，纳入 `make test` 与 `scripts/test-windows.ps1` 的门禁集。

### 8.1 结构性不变量

| 用例 | 断言 |
|---|---|
| `TestTranscriptCoversHandshakeFrames` | 反射遍历 `Register` 与 `RegisterChallenge` 的全部 JSON 字段名，断言每个都出现在 `HandshakeTranscript` 中（`client_id`↔`label`、`type`↔`peer_type` 两处别名显式列出映射表）。新增握手字段而忘记入 transcript 时直接失败 |

这条测试是本次修复中最重要的**防回归**资产 —— 缺陷的根因不是某行代码写错，而是"transcript 该覆盖什么"没有被机器强制。

### 8.2 重放与篡改

| 用例 | 断言 |
|---|---|
| `TestReplayRecordedServerStreamRejected` | §3.1 的完整 PoC：录制真实会话 → 改写 `expires_at` 重放 → 断言客户端在握手阶段失败 |
| `TestReplayRecordedServerStreamRejectedWithinExpiryWindow` | 同上但**不改写** `expires_at`，在原始有效期内立即重放 → 断言仍失败（这一条正是 §4 中最小补丁挡不住的场景） |
| `TestChallengeFieldTamperRejected` | 逐字段篡改 `expires_at` / `algorithm` / `server_share` / `session_id` / `handshake_id` / `challenge` → 全部导致握手失败 |
| `TestClientShareTamperRejected` | 中间人替换 `register` 中的 `client_share` → 服务端 proof 验证失败 |

### 8.3 ECDH 与编码

| 用例 | 断言 |
|---|---|
| `TestPeerShareEncodingRejected` | 非规范 base64、长度 ≠ 32、空串 → 拒绝 |
| `TestLowOrderPeerShareRejected` | 已知低阶点（全零、order-8 点）→ `ComputeSharedSecret` 返回错误。这条同时充当对 `crypto/ecdh` 全零拒绝行为的假设固化，必须在动手实现前先单独跑通 |
| `TestSessionKeysDifferAcrossHandshakes` | 同一凭据连续两次握手 → 三把密钥两两不同（前向保密的可观察前提） |
| `TestDeriveSessionKeysRejectsBadSharedLength` | `shared` 长度 ≠ 32 → 错误 |

### 8.4 认证与枚举

| 用例 | 断言 |
|---|---|
| `TestUnknownLabelIndistinguishable` | 未知 label 与"已知 label + 错误秘密"两条路径：均收到 challenge、均以 `authentication_failed` 结束、均不产生 peer |
| `TestDecoyAuthenticationNeverProducesPeer` | decoy 路径下 `PrincipalID` 为空，`reservePeer` 永不被调用 |
| `TestAuthenticatorMissingIsHardFailure` | 未配置 authenticator 仍然硬失败，不落入 decoy |

### 8.5 保留的现有覆盖

`wss` 与 `hub` 现有测试全部保留，其中原始 WebSocket 帧断言（token / application key / hostname / work_dir 不出现在明文）必须继续通过，并**增加断言**：`client_share` 与 `server_share` 出现在明文中是预期的（公钥公开无害），但 ephemeral 私钥的任何字节都不得出现。

### 8.6 进程级 E2E

现有 Mind/Hand/Face 真实进程 E2E（动态端口、临时 HOME、Scripted LLM）在版本切换后必须整体通过，额外补一条：以 v2 二进制连接 v3 Mind，断言收到 `unsupported_protocol` 且不重试。

---

## 9. 不在本次范围内

以下三项已识别但不应混入本次修复，各自独立排期：

### 9.1 静态非对称身份（v4 候选）

v3 之后 PSK 仍是对称的：Mind 的 SQLite 泄露后，攻击者可以**冒充 Mind** 面向任一 Hand（虽然不再能解密历史流量）。彻底的做法是每个 peer 持有静态 X25519 密钥对，Mind 只存公钥，Mind 自身也有静态密钥对由客户端固定（pinning）—— 即 Noise `IK` 模式。这会让数据库泄露不再等价于完整冒充能力。

这是正确的终局，但它改变凭据模型、管理 CLI、`/hand add` 输出形态和用户心智，必须独立设计。v3 引入的 ECDH 代码是它的前置基础。

### 9.2 传输层 TLS

应用层加密提供了机密性与完整性，但没有提供元数据保护（peer type、label、时序、长度全部明文可见）。建议在文档中明确推荐生产部署把 Mind 置于反向代理之后走 `wss://`，并把这一点写进 `ai-face-protocol.md` 的部署章节。Face 侧配置已经支持 `wss://`（`half-pi-face/internal/config/config.go:94`）。

### 9.3 握手速率限制

当前没有失败节流或锁定。§5.7 的 `pendingConns` 上限只挡并发，不挡持续暴力。按来源 IP 的失败计数与退避是合理的后续项，但需要先明确反向代理场景下的真实来源识别（`X-Forwarded-For` 信任边界），不宜草率实现。

---

## 10. 验收清单

- [x] `TestTranscriptCoversHandshakeFrames` 通过；改掉 `RegisterChallenge.expires_at` 的 json tag 后确认失败（变异验证）
- [x] §8.2 重放用例通过：改写 `expires_at` 与"有效期窗口内原样重放"两条均被拒绝，且已确认修复前两条都失败
- [x] ECDH 共享秘密确实参与派生：`TestSharedSecretIsRequiredForDerivation`，去掉 `root = append(root, shared...)` 后确认失败（变异验证）
- [x] 同凭据连续两次握手的会话密钥互不相同
- [x] 未知 label 与已注册 label 在 proof 前不可区分：`TestUnknownLabelIsNotDistinguishableBeforeProof`，恢复提前拒绝后确认失败（变异验证）
- [x] `make test` 五模块全绿（`-race`），含 Mind/Hand/Face 进程级 E2E
- [x] 原始帧断言：双秘密不出现在明文；`NewEphemeralShare` 上线公钥而非私钥种子
- [x] `AGENTS.md` 决策记录、`docs/face-protocol.md`、`docs/ai-face-protocol.md`、`wiki/` 当前行为描述已更新
- [ ] v2↔v3 双向 fail closed 的**跨版本二进制**验证：版本检查与 `StrictDecode` 路径已有单元覆盖，但未实际用 v2 二进制对 v3 Mind 跑一遍
- [ ] `scripts/test-windows.ps1 -PrebuiltDir` 原生通过（需 Windows 环境，本次未执行）
- [ ] 发布说明写明：无需重新签发凭据，但需要全节点同步升级
