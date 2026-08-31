# Thiết kế bộ nhớ đệm prompt: phối hợp ba tầng litellm / agentcore / ainovel

> Tài liệu này là một tài liệu giải thích: giới thiệu cách chúng ta thiết kế bộ nhớ đệm prompt LLM
> (prompt caching) đầu cuối trong ba kho cộng tác, bao gồm nguyên lý thiết kế, các ca điều tra thực tế
> và vị trí mã nguồn có thể đối chiếu.
>
> - **litellm** —— Cổng LLM: dịch giao thức và khai báo năng lực
> - **agentcore** —— Framework Agent: đặt cache và định danh cache
> - **ainovel-cli** —— Tầng ứng dụng: tích hợp bằng một dòng cấu hình (codebot cũng tương tự)

---

## 1. Vì sao đáng làm: mô hình chi phí và một ca thực tế

Yêu cầu của hệ thống Agent có một đặc điểm cấu trúc: **mỗi vòng yêu cầu đều mang theo toàn bộ lịch sử**. Một vòng lặp công cụ 30 lượt,
trong thân yêu cầu của lượt thứ 30 có chứa toàn bộ thông điệp của 29 lượt trước. Nếu không dùng cache, cùng một đoạn byte tiền tố sẽ bị tính phí lặp đi lặp lại.

Định giá cache của hai nhà cung cấp lớn (lấy Anthropic làm ví dụ):

| Hạng mục | Giá tương đối so với input thông thường |
|---|---|
| Ghi cache (TTL 5 phút) | 1.25x |
| Ghi cache (TTL 1 giờ) | 2x |
| **Đọc cache** | **0.1x (tiết kiệm 90%)** |

Ca thực tế: một lần sinh tiểu thuyết dài 33 chương đã tiêu tốn $58; sau đó phân tích `meta/usage.json` phát hiện
**tỷ lệ cache hit tổng thể chỉ 8.5%** (coordinator chỉ 2.7%, architect là 0). Sau khi đối chiếu từng yêu cầu trong
chuỗi usage (input vs cache_read), đã định vị được ba nguyên nhân gốc:

1. **tools bị dao động byte**: Description/Schema của công cụ subagent mỗi vòng được dựng lại bằng cách lặp trực tiếp từ Go map,
   thứ tự ngẫu nhiên → thân yêu cầu khác với lượt trước ngay từ byte 0 → toàn bộ cache tiền tố mất hiệu lực;
2. **không có tính thân hòa định tuyến**: hệ OpenAI không truyền `prompt_cache_key`, nên ngay cả các yêu cầu có byte hoàn toàn giống nhau cũng có thể bị
   cân bằng tải sang instance không có cache (bằng chứng rõ ràng: trong 33 session, yêu cầu đầu tiên có byte giống nhau chỉ hit 12 cái);
3. **hệ Claude không có breakpoint**: Anthropic là cache tường minh; không đặt breakpoint `cache_control` = hoàn toàn không có cache.

Ba nguyên nhân gốc này lần lượt tương ứng với ba phần thiết kế bên dưới: **kỷ luật ổn định tiền tố**, **định danh cache**, **điều phối breakpoint**.

---

## 2. Kiến thức chuẩn bị: mô hình tư duy của hai giao thức cache

### 2.1 OpenAI: cache tiền tố tự động (ẩn)

- Máy chủ tự động cache tiền tố **≥1024 tokens**, không cần client khai báo;
- Hit tăng theo độ hạt căn chỉnh 128-token;
- Yêu cầu có thể mang `prompt_cache_key` (trường chính thức) để tạo **tính thân hòa định tuyến** —— các yêu cầu cùng key sẽ cố gắng rơi vào
  cùng một phân mảnh cache;
- Trong usage, `cached_tokens` báo cáo lượng hit; **ghi cache không bao giờ được báo cáo** (`cache_write` luôn bằng 0
  là hiện tượng bình thường, không phải bug).

### 2.2 Anthropic: breakpoint tường minh (cache_control)

- Client đặt breakpoint `cache_control` trên khối nội dung, **breakpoint bao phủ mọi thứ phía trước nó**
  (thứ tự cố định là tools → system → messages);
- Mỗi yêu cầu **tối đa 4 breakpoint**;
- Giá ghi 1.25x (5m) / 2x (1h), giá đọc 0.1x;
- `cache_control` **không được đặt trên khối thinking** (sẽ bị từ chối 400).

### 2.3 Tiền đề chung

Dù là ẩn hay tường minh, cache chỉ nhận **tiền tố bằng nhau ở cấp byte**. Vì vậy nền móng của mọi thiết kế là cùng một câu:

> **Sắp xếp toàn bộ yêu cầu theo tần suất thay đổi từ thấp đến cao: phần tĩnh đặt ở trước nhất, phần động đặt ở cuối cùng,
> và lịch sử đã gửi không được thay đổi dù chỉ một byte.**

---

## 3. Kiến trúc tổng thể: phân công ba tầng

```
┌────────────────────────────────────────────────────────┐
│ Tầng ứng dụng（ainovel-cli / codebot）                   │
│   Quyết định "định danh cache" lấy giá trị nào: mỗi sách một gốc, mỗi vai trò một tên │
│   Chi phí tích hợp = mỗi agent hai dòng cấu hình         │
├────────────────────────────────────────────────────────┤
│ agentcore（Framework Agent）                             │
│   Quyết định "đặt breakpoint ở đâu, khi nào sinh key":   │
│   sàn system + đầu nhọn cuộn ở thông điệp cuối; spawn thêm #seq; │
│   Gated theo năng lực provider, không hỗ trợ thì lặng lẽ bỏ │
├────────────────────────────────────────────────────────┤
│ litellm（Cổng LLM）                                      │
│   Chỉ dịch giao thức: cache_control ↔ trường của từng nhà cung cấp, │
│   truyền xuyên prompt_cache_key, khai báo năng lực Capabilities │
│   Không đưa ra bất kỳ quyết định "có nên cache hay không" nào │
└────────────────────────────────────────────────────────┘
```

Nguyên tắc chia tách: **litellm chỉ trả lời "endpoint này hỗ trợ gì", agentcore chỉ trả lời "đặt điểm cache ở đâu",
tầng ứng dụng chỉ trả lời "định danh là gì"**. Mỗi tầng có thể kiểm thử độc lập; nếu đổi ứng dụng khác
(codebot tái sử dụng cùng bộ agentcore/litellm) thì không cần viết lại logic cache.

---

## 4. Nền móng: ba kỷ luật ổn định byte tiền tố

Điều kiện tiên quyết để cache có lợi là byte tiền tố ổn định. Ba kỷ luật tương ứng với ba sự cố thực tế.

### Kỷ luật một: tuần tự hóa tools phải xác định ở cấp byte

Sự cố: công cụ `subagent` nhúng danh sách agent đã đăng ký vào Description/Schema của chính nó, mà danh sách này đến từ
việc lặp Go map —— mỗi lần gọi thứ tự ngẫu nhiên, byte của tools mỗi vòng đều đổi, khiến tỷ lệ hit của coordinator chỉ còn 2.7%.
(Nhóm Claude Code cũng từng bị đúng vấn đề này cắn: toàn fleet của họ từng vì thế mà trả thêm 10.2% phí ghi cache.)

Sửa chữa (agentcore `subagent/subagent.go`):

```go
// sortedAgentNames trả về tên agent đã đăng ký theo thứ tự xác định.
// Description và Schema được dựng lại ở mỗi lần gọi LLM; lặp trực tiếp map
// sẽ làm xáo trộn byte của chúng giữa các yêu cầu và phá vỡ cache tiền tố
// của provider (tools được tuần tự hóa vào tiền tố prompt được cache).
func (t *Tool) sortedAgentNames() []string {
	return slices.Sorted(maps.Keys(t.agents))
}
```

> Dạng tổng quát của bài học: **bất kỳ tập hợp nào đi vào thân yêu cầu đều phải được sắp xếp trước khi tuần tự hóa**. Việc lặp map của Go
> bị ngẫu nhiên hóa sẽ giấu bug này rất sâu —— chức năng hoàn toàn bình thường, chỉ có hóa đơn là bất thường.

### Kỷ luật hai: lịch sử phải append-only (nén phải được "commit")

Sự cố: chiến lược nén ngữ cảnh của writer là "projection" (mỗi lần gọi tạm thời viết lại view lịch sử, nhưng không ghi về
baseline). Một khi vượt ngưỡng, **mỗi vòng đều viết lại toàn bộ tiền tố** → vòng nào cũng miss hoàn toàn.

Sửa chữa: commit sau khi projection (`CommitOnProject: true`), để việc viết lại chỉ xảy ra một lần, sau đó khôi phục
append-only cho đến lần tiếp theo vượt ngưỡng.

> Dạng tổng quát: nén ngữ cảnh là **một lần đứt gãy có kế hoạch** (reset tiền tố, trả toàn giá một lần),
> điều này không sao; điều không thể chấp nhận là **vòng nào cũng đứt**. Nén hoặc là không làm, hoặc làm xong thì cố định hóa.

### Kỷ luật ba: nội dung động đi vào phần đuôi

Những thứ thay đổi mỗi vòng (phong bì trạng thái thế giới, nhắc nhở theo từng vòng, kết quả công cụ mới nhất) chỉ được phép **append ở đuôi thông điệp**,
tuyệt đối không quay lại sửa phần giữa. Phong bì `novel_context` của ainovel chính là thiết kế append ở đuôi —— nó thay đổi mỗi chương,
nhưng sự thay đổi đó không ảnh hưởng tới cache của hàng trăm nghìn token phía trước.

---

## 5. Định danh cache: mỗi sách một gốc, mỗi vai trò một tên, mỗi session một key

`prompt_cache_key` của hệ OpenAI giải quyết **vấn đề định tuyến**: nếu các yêu cầu có byte giống nhau bị cân bằng tải sang
các instance khác nhau thì vẫn miss. Mục tiêu thiết kế key là "các yêu cầu thuộc cùng một huyết thống cache luôn mang cùng một key".

Định danh ba cấp của chúng ta (ainovel `internal/agents/build.go`):

```go
// promptCacheBase sinh hash ngắn ổn định từ thư mục sách, làm tiền tố định danh cache prompt:
// cùng một cuốn sách chia sẻ bucket định tuyến qua các lần khởi động lại tiến trình, và không
// rò rỉ đường dẫn cục bộ cho provider. Hậu tố vai trò do bên gọi nối thêm,
// subagent mỗi lần spawn lại thêm "#seq" (mỗi session một key).
func promptCacheBase(bookDir string) string {
	sum := sha256.Sum256([]byte(bookDir))
	return "nvl-" + hex.EncodeToString(sum[:6])
}
```

Tích hợp ở tầng ứng dụng chỉ là mỗi agent hai dòng:

```go
writer := subagent.Config{
	// ...
	CacheLastMessage: "ephemeral",                // Công tắc breakpoint Claude (xem §6)
	PromptCacheKey:   cacheBase + "-writer",      // Định danh định tuyến OpenAI (cấp vai trò)
}
// coordinator（Agent tầng trên）cũng tương tự:
agentcore.WithCacheLastMessage("ephemeral"),
agentcore.WithPromptCacheKey(cacheBase+"-coordinator"),
```

Cấp thứ ba (cấp session) do agentcore tự động sinh —— mỗi lần spawn một session mới là một
huyết thống cache mới (agentcore `subagent/subagent.go`):

```go
runSeq := t.runSeq.Add(1)

// Một cuộc hội thoại, một cache key: thêm hậu tố sequence theo từng lần chạy để mỗi
// spawn hình thành huyết thống cache riêng, thay vì dồn mọi lần chạy của agent này
// vào một bucket định tuyến duy nhất.
promptCacheKey := cfg.PromptCacheKey
if promptCacheKey != "" {
	promptCacheKey = fmt.Sprintf("%s#%d", promptCacheKey, runSeq)
}
```

Hình thái cuối cùng: `nvl-a1b2c3-writer#17` = cuốn sách này, vai trò writer, session spawn lần thứ 17.

> Vì sao không dùng một key toàn cục? Tiền tố của các session khác nhau là khác nhau; trộn vào cùng một bucket định tuyến sẽ làm loãng hit.
> Vì sao không mang timestamp/số ngẫu nhiên? key phải **ổn định xuyên suốt các yêu cầu**, mỗi vòng trong session đều phải giống nhau.

Thiết kế tương ứng của codebot: ngữ nghĩa key = SessionID (đổi session = đổi huyết thống), teammate thêm hậu tố tên,
host khi tái sử dụng cùng một instance Agent để đổi session thì gọi `Agent.SetPromptCacheKey` để trỏ lại định danh.

---

## 6. Điều phối breakpoint Claude: sàn + đầu nhọn cuộn

Anthropic không đặt breakpoint = không có cache. Phân bổ ngân sách của chúng ta (giới hạn 4 breakpoint/yêu cầu):

```
[tools][system ←breakpoint①"sàn"][...thông điệp lịch sử...][thông điệp mới nhất ←breakpoint②"đầu nhọn cuộn"]
```

### 6.1 Sàn (floor): ghim tiền tố tĩnh

system prompt là khối tĩnh lớn nhất. Cấp cho nó một breakpoint riêng, đảm bảo **khi session mới/đoạn cache đuôi bị trục xuất,
ít nhất tiền tố system+tools vẫn được đọc từ cache** (agentcore `loop.go`):

```go
} else if agentCtx.SystemPrompt != "" {
	m := SystemMsg(agentCtx.SystemPrompt)
	if config.CacheLastMessage != "" {
		// Sàn cache: ghim system prompt tĩnh bằng breakpoint riêng
		// để một session mới — hoặc một lượt mà entry đuôi đã bị
		// trục xuất — vẫn đọc tiền tố system+tools từ cache.
		m.Metadata = map[string]any{"cache_control": config.CacheLastMessage}
	}
	prefix = append(prefix, m)
}
```

### 6.2 Đầu nhọn cuộn (rolling tip): mỗi vòng đẩy phạm vi bao phủ tiến lên

Đặt một breakpoint trên **thông điệp không phải system cuối cùng**. Trong vòng lặp công cụ, mỗi lần gọi LLM đều sẽ ghi một cache
bao phủ tới tool_use+tool_result mới nhất; vòng tiếp theo đọc trực tiếp, không cần truyền lại:

```go
// markLastMessageForCache trả về một bản sao của messages với cache_control được gắn
// vào metadata của thông điệp không phải system cuối cùng. Bỏ qua system message để
// các nhắc nhở theo lượt ở đuôi (thay đổi mỗi lượt) không mang breakpoint.
func markLastMessageForCache(messages []Message, cacheControl string) []Message {
	idx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != RoleSystem {
			idx = i
			break
		}
	}
	// ...
}
```

Chú ý bỏ qua system reminder ở đuôi: nó thay đổi mỗi vòng; đặt breakpoint trên nó tương đương với mỗi vòng ghi một cache
sẽ không bao giờ được tái sử dụng.

### 6.3 Ngữ nghĩa khối cuối: một thông điệp chỉ tiêu một breakpoint

Ngữ nghĩa `cache_control` cấp thông điệp là "ghi một breakpoint sau thông điệp này". Khi dịch sang cấp block, chỉ được phép
đặt trên **khối cuối cùng có thể cache** —— nếu đánh dấu mọi block sẽ đốt hết ngân sách 4 breakpoint; còn Anthropic
từ chối khối thinking mang `cache_control`, nên quét từ đuôi và bỏ qua reasoning
(agentcore `llm/litellm.go`):

```go
if cache != nil {
	// Anthropic từ chối cache_control trên các khối thinking — đặt
	// breakpoint trên khối có thể cache cuối cùng thay vào đó.
	for i := len(blocks) - 1; i >= 0; i-- {
		if _, isReasoning := blocks[i].(litellm.ReasoningBlock); isReasoning {
			continue
		}
		blocks[i] = withBlockCache(blocks[i], cache)
		break
	}
}
```

### 6.4 Đường ống TTL

Giá trị cấu hình được quy ước là chuỗi `"type[:ttl]"`, ví dụ `"ephemeral"` (mặc định 5m) hoặc `"ephemeral:1h"`:

```go
func cacheControlFromMetadata(metadata map[string]any) *litellm.CacheControl {
	value, _ := metadata["cache_control"].(string)
	if value == "" {
		return nil
	}
	if typ, ttl, ok := strings.Cut(value, ":"); ok {
		return &litellm.CacheControl{Type: typ, TTL: ttl}
	}
	return &litellm.CacheControl{Type: value}
}
```

Có nên nâng lên 1h hay không phải nói bằng dữ liệu: giá ghi tăng từ 1.25x lên 2x, chỉ khi khoảng cách gọi thực đo thường xuyên vượt 5 phút
mới đáng (chúng ta đo thực tế median interval của coordinator là 172s, nên không nâng).

---

## 7. Gửi an toàn: gating theo năng lực + xác định endpoint chính thức

### 7.1 Gating theo năng lực: trường không được hỗ trợ không được gửi ra ngoài

Các provider của litellm **kiểm tra nghiêm ngặt** `ProviderOptions` (key không biết sẽ báo lỗi trực tiếp), vì vậy
agentcore gating theo khai báo năng lực trước khi gửi (agentcore `llm/litellm.go`):

```go
// Định danh định tuyến prompt-cache. Được gating theo năng lực: các provider litellm
// kiểm tra provider options nghiêm ngặt, nên một key không được hỗ trợ phải được
// bỏ tại đây thay vì bị từ chối ở đó.
if callCfg.PromptCacheKey != "" && caps.Cache.PromptKey == litellm.SupportYes {
	req.ProviderOptions["prompt_cache_key"] = callCfg.PromptCacheKey
}
```

### 7.2 Xác định endpoint chính thức: hệ sinh thái tương thích không có hợp đồng trường chưa biết

`prompt_cache_key` là trường chính thức của OpenAI, nhưng hành vi của endpoint "tương thích OpenAI" không có bất kỳ hợp đồng thống nhất nào.
Bằng chứng thực nghiệm qua mạng (2026-07):

- **Endpoint nghiêm ngặt từ chối trực tiếp**: Groq, Cerebras, Volcano Engine, Fireworks trả về 400/422 cho trường này
  (Zed #36215, OpenClaw #48155 đều vì vậy mà đổi sang gửi có điều kiện);
- **Trung chuyển kiểu tái đóng gói âm thầm bỏ qua**: đường dẫn không truyền xuyên của one-api/new-api/sub2api parse thân yêu cầu vào
  struct rồi re-marshal, trường chưa biết biến mất không tiếng động (gửi cũng như không);
- **Endpoint lỏng bỏ qua**: Ollama, vLLM phiên bản hiện tại, MiniMax.

Vì vậy khai báo năng lực của litellm openai provider được phán định **động** theo BaseURL
(litellm `provider/openai/capabilities.go`):

```go
// promptCacheParamsSupport báo cáo endpoint này có đáng tin để chấp nhận
// params cache prompt của OpenAI (prompt_cache_key / prompt_cache_retention) hay không.
// Chỉ endpoint chính thức mới đảm bảo hợp đồng trường.
func (p *Provider) promptCacheParamsSupport() litellm.Support {
	if p.cfg.PromptCacheParams || isOfficialBaseURL(p.cfg.BaseURL) {
		return litellm.SupportYes
	}
	return litellm.SupportUnknown
}func isOfficialBaseURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "api.openai.com")
}
```

Chính thức `api.openai.com` → `SupportYes` (gửi); BaseURL bên thứ ba → `SupportUnknown`
(cổng kiểm soát ở §7.1 sẽ tự động không gửi, **mặc định không bao giờ làm nổ bất kỳ endpoint nào**); người dùng xác nhận relay của mình truyền nguyên dạng,
có thể opt-in tường minh trong cấu hình provider:

```jsonc
"my-relay": {
  "type": "openai",
  "base_url": "https://relay.example.com/v1",
  "extra": { "prompt_cache_params": true }   // Tôi xác nhận relay này truyền nguyên dạng request body
}
```

> Vì sao công tắc đặt ở tầng năng lực litellm thay vì tầng cấu hình ứng dụng? Vì runtime `/model` đổi provider
> sẽ đổi client, khai báo năng lực sẽ tự động đổi theo client; phán định ở giai đoạn dựng ứng dụng không bao phủ được việc đổi runtime.

---

## 8. Quan sát: phát hiện đứt gãy chuỗi cache

Cache là "tính năng vô hình" — hỏng không báo lỗi, chỉ trở nên đắt hơn. Vì vậy cần có quan sát (tham khảo
promptCacheBreakDetection của Claude Code, làm một phiên bản nhẹ).

Tiêu chí phán định (ainovel `internal/host/usage.go`):

```go
// Trong cùng một phiên (role+task): tiền tố không ngắn đi, nhưng lượng hit giảm >5% so với lần trước và mức giảm ≥2000 tokens
broke := prevPrefix > 0 && prefix >= prevPrefix &&
	float64(u.CacheRead) < float64(prevRead)*cacheBreakKeepRatio &&
	prevRead-u.CacheRead >= cacheBreakMinDropTokens
```

Bốn thiết kế then chốt, mỗi thiết kế tương ứng với một loại báo nhầm:

| Thiết kế | Báo nhầm được phòng tránh |
|---|---|
| **Ngưỡng kép** (tương đối 5% và tuyệt đối 2000) | Ngưỡng tương đối đơn lẻ bị nhiễu tiền tố nhỏ nhấn chìm; ngưỡng tuyệt đối đơn lẻ bỏ sót suy thoái ở tiền tố lớn |
| **Baseline đi theo phiên (role+task)** | Chiều phát hiện phải căn khớp với độ hạt phiên (`#seq`) của `prompt_cache_key`; so sánh theo role xuyên phiên sẽ báo nhầm khi "phiên trước rất ngắn, request đầu của phiên mới có tiền tố còn dài hơn" (lỗ hổng thật do Codex review bắt được) |
| **Tiền tố ngắn đi = reset hợp lệ** | Nén ngữ cảnh là đứt gãy có kế hoạch, reset baseline và không cảnh báo |
| **replay không phát hiện** | Replay lịch sử khi khởi động sẽ biến các đứt gãy cũ thành cảnh báo mới |

Khi cảnh báo, đưa gợi ý quy nguyên nhân theo khoảng thời gian: khoảng cách >1h → nghi ngờ TTL 1h hết hạn; >5m → nghi ngờ TTL 5m hết hạn;
rất ngắn → nghi ngờ server-side eviction / route drift (**relay luân phiên tài khoản upstream là nguyên nhân phổ biến nhất**). Bộ đếm được lưu bền vào
`usage.json` và hiển thị ở dòng "đứt gãy liên kết" trong panel cache của TUI.

---

## 9. Lằn ranh latch: nguyên tắc đơn điệu của phiên

Một ràng buộc cấp hiến pháp cho các tính năng tương lai:

> **Mọi lượng sẽ đi vào tiền tố cache (system prompt, tools, tham số thinking, tham số sampling),
> sau khi được tính lần đầu trong phiên thì phải đóng băng — thà cũ, không được phá cache.**

Ví dụ: các tính năng kiểu "điều chỉnh cường độ thinking lúc runtime", nếu để cường độ mới tác dụng ngay lên phiên đang chạy,
thì tương đương mỗi lần điều chỉnh đều viết lại tiền tố, làm vô hiệu toàn bộ cache. Cách đúng là giá trị mới chỉ có hiệu lực với **phiên được spawn mới**.
Với bất kỳ yêu cầu "runtime có thể chỉnh X" nào, câu hỏi đầu tiên khi review đều là: X có nằm trong tiền tố cache không?

---

## 10. Ngộ nhận thường gặp và trần giới hạn

1. **`cache_write` của OpenAI luôn là 0 là bình thường** — API không báo cáo lượng ghi, đừng điều tra như bug.
2. **Trần của relay**: nếu relay luân phiên nhiều tài khoản upstream, request phía client có ổn định từng byte cũng vẫn miss (cache của tài khoản upstream A
   không hiển thị với tài khoản B). Điều này giải thích bí ẩn "request giống hệt từng byte mà chỉ hit 12/33".
   **Đây không phải vấn đề phía client có thể giải** — dữ liệu của team Claude Code cũng cho thấy khoảng chín mươi phần trăm case "client không thay đổi
   nhưng vẫn đứt gãy" là do phía server.
3. **Tiêu chí xác minh**: JSONL phiên không chứa system prompt và request body đầy đủ, **chuỗi usage theo từng request
   (input vs cache_read) mới là chuẩn vàng để chẩn đoán**. Một fingerprint thực dụng: nếu lượng hit đóng đinh đúng ở
   "số token system prompt làm tròn xuống theo 128", nghĩa là chỉ segment system hit, toàn bộ segment message miss.
4. **Hạch toán lợi ích**: giá đọc 0.1x, giá ghi 1.25x, nghĩa là một cache chỉ cần được đọc 1 lần là hoàn vốn.
   Trong phiên agent nhiều vòng, breakpoint hầu như luôn có lợi ròng, nên `CacheLastMessage` không đặt công tắc, mặc định bật.

---

## 11. Tra cứu nhanh hướng dẫn tích hợp

**ainovel-cli** (đã tích hợp sẵn): mỗi agent cấu hình `CacheLastMessage: "ephemeral"` +
`PromptCacheKey: promptCacheBase(bookDir) + "-<role>"`, phần còn lại hoàn toàn tự động.

**codebot** (đã tích hợp sẵn): key = SessionID; khi `Reset`/`SwitchSession`
`agent.SetPromptCacheKey(newSessionID)`; teammate dùng `sessionID + "-" + name`.

**Checklist tối thiểu cho ứng dụng mới tích hợp agentcore**:

```go
agentcore.NewAgent(
	agentcore.WithCacheLastMessage("ephemeral"),   // Breakpoint Claude: sàn + đỉnh lăn
	agentcore.WithPromptCacheKey(stableIdentity),  // Định tuyến OpenAI: ổn định, duy nhất theo từng phiên
	// ...
)
```

Kèm ba câu tự kiểm tra (tương ứng ba kỷ luật):

1. Việc serialize tools của tôi có xác định từng byte không? (các tập hợp đã được sort chưa)
2. Lịch sử của tôi có append-only không? (nén có được commit không)
3. Nội dung thay đổi mỗi vòng của tôi đều ở phần đuôi không?

---

## 12. Danh sách kinh nghiệm cho người học

- Bản chất của tối ưu cache là **kỷ luật byte**, không phải chỉnh tham số: trước tiên bảo đảm tiền tố ổn định, rồi mới nói tới key và breakpoint.
- Chẩn đoán luôn bắt đầu từ **chuỗi usage theo từng request**, đừng đoán từ code.
- Go map iterator bị random hóa + serialize request body = sát thủ cache kín đáo nhất, test chức năng sẽ không bao giờ phát hiện.
- "OpenAI compatible" là lời tiếp thị, không phải hợp đồng: trước khi gửi field chính thức tới endpoint bên thứ ba, hãy tìm bằng chứng trực tiếp
  (source code/issue/bản sửa đã triển khai của client cùng loại), "thường sẽ ignore" là suy đoán nguy hiểm.
- Quan sát phải ưu tiên chống báo nhầm: chiều phát hiện phải căn khớp với độ hạt của huyết thống cache; thà bỏ sót còn hơn báo nhầm,
  nếu không cảnh báo sẽ nhanh chóng bị phớt lờ.
- Tiêu chuẩn kiểm nghiệm phân tầng: khi tích hợp một ứng dụng khác (codebot), logic cache không cần viết lại dòng nào.

---

### Phụ lục: chỉ mục mã nguồn

| Chủ đề | Vị trí |
|---|---|
| Sort xác định cho tools | agentcore `subagent/subagent.go` `sortedAgentNames` |
| Dẫn xuất key cấp phiên (#seq) | agentcore `subagent/subagent.go` `runAgent` |
| Sàn system + đỉnh lăn | agentcore `loop.go` `callLLM` / `markLastMessageForCache` |
| Breakpoint block cuối + bỏ qua thinking | agentcore `llm/litellm.go` `convertAgentBlocks` |
| Phân tích TTL (`"ephemeral:1h"`) | agentcore `llm/litellm.go` `cacheControlFromMetadata` |
| Cổng kiểm soát năng lực | agentcore `llm/litellm.go` `applyCallConfig` |
| Phán định endpoint chính thức + opt-in | litellm `provider/openai/capabilities.go` / `provider.go Config` |
| Định danh cache (một sách một base) | ainovel `internal/agents/build.go` `promptCacheBase` |
| Phát hiện đứt gãy | ainovel `internal/host/usage.go` `noteCacheBreak` |
| Định vị kiến trúc | ainovel `docs/architecture.md` §6.6 |