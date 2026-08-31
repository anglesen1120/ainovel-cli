// Package llmcontract là tầng contract và thực thi thống nhất cho trả về cấu trúc trực tiếp: Contract tĩnh
// là nguồn duy nhất của cấu trúc; Execute thống nhất chọn capability, chuẩn bị prompt, retry request,
// decode Schema/DTO và tự phục hồi bằng phản hồi.
// Giao thức được xác định trước khi gửi request; khi request native bị từ chối hoặc vi phạm contract thì lộ nguyên trạng, cấm âm thầm bỏ schema để gửi lại.
package llmcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

// Contract là contract tĩnh của một lần trả về cấu trúc trực tiếp, đặt cạnh định nghĩa DTO ở từng biên.
type Contract struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Mode là giao thức cấu trúc được dùng cho lần gọi này.
type Mode string

const (
	ModeNativeJSONSchema Mode = "native_json_schema"
	ModePromptContract   Mode = "prompt_contract"
)

// Source là nguồn căn cứ để phán định capability.
type Source string

const (
	SourceConfig  Source = "config"  // người dùng khai báo rõ trong ModelConfig.json_schema
	SourceAdapter Source = "adapter" // bảng capability cấp mô hình của provider adapter
	SourceUnknown Source = "unknown" // không khai báo và capability chưa biết, bảo thủ dùng prompt contract
)

// Resolution là kết quả chọn giao thức trước khi gửi request, cho bên gọi phân nhánh và ghi log.
type Resolution struct {
	Mode     Mode
	Source   Source
	Strict   bool // khi native có kèm strict hay không
	Provider string
	Model    string
}

// jsonSchemaOverrider do wrapper mô hình mang override ba trạng thái config implement
// (bootstrap.SwappableModel và các tầng wrapper truyền xuyên nó).
type jsonSchemaOverrider interface {
	JSONSchemaOverride() *bool
}

type modelInfoProvider interface {
	Info() llm.ModelInfo
}

// ModelFacts là snapshot cùng thời điểm cần cho một lần phân tích capability. Wrapper hot-swap implement interface này,
// tránh Resolve đọc riêng capability, override config và định danh mô hình rồi lẫn trạng thái giữa hai lần chuyển đổi.
type ModelFacts struct {
	Capabilities       llm.Capabilities
	Info               llm.ModelInfo
	JSONSchemaOverride *bool
}

type modelFactsProvider interface {
	StructuredOutputFacts() ModelFacts
}

// Resolve đọc facts mô hình hiện tại ở mỗi lần gọi (sau hot-swap, lần gọi kế tiếp dùng giá trị mới):
// ba trạng thái config ưu tiên trước, sau đó là capability cấp mô hình của adapter; chưa biết thì luôn dùng prompt contract.
func Resolve(model any) Resolution {
	res := Resolution{Mode: ModePromptContract, Source: SourceUnknown}

	var caps llm.Capabilities
	var info llm.ModelInfo
	var override *bool
	if fp, ok := model.(modelFactsProvider); ok {
		facts := fp.StructuredOutputFacts()
		caps, info, override = facts.Capabilities, facts.Info, facts.JSONSchemaOverride
	} else {
		if cp, ok := model.(llm.CapabilityProvider); ok {
			caps = cp.Capabilities()
		}
		if ip, ok := model.(modelInfoProvider); ok {
			info = ip.Info()
		}
		if o, ok := model.(jsonSchemaOverrider); ok {
			override = o.JSONSchemaOverride()
		}
	}
	res.Provider, res.Model = caps.Provider, caps.Model
	if res.Provider == "" {
		res.Provider = info.Provider
	}
	if res.Model == "" {
		res.Model = info.Name
	}

	if override != nil {
		res.Source = SourceConfig
		if *override {
			res.Mode = ModeNativeJSONSchema
			// Người dùng khai báo endpoint tuân thủ contract Structured Outputs thì mặc định strict;
			// chỉ khi adapter nói rõ không hỗ trợ strict mới chỉ gửi schema mà không gửi strict.
			res.Strict = caps.Structured.Strict != llm.SupportNo
		}
		return res
	}

	switch caps.Structured.JSONSchema {
	case llm.SupportYes:
		res.Mode = ModeNativeJSONSchema
		res.Source = SourceAdapter
		res.Strict = caps.Structured.Strict == llm.SupportYes
	case llm.SupportNo:
		res.Source = SourceAdapter
	}
	return res
}

// Plan phân tích giao thức và sinh call option trong native mode; prompt contract mode trả nil opts.
func Plan(model any, c Contract) ([]agentcore.CallOption, Resolution) {
	res := Resolve(model)
	if res.Mode != ModeNativeJSONSchema {
		return nil, res
	}
	return []agentcore.CallOption{
		agentcore.WithJSONSchema(c.Name, c.Description, c.Schema, res.Strict),
	}, res
}

// PreparePrompt giữ chỉ một prompt ngữ nghĩa nghiệp vụ: native mode trả thẳng nguyên văn; prompt
// contract mode tự sinh hậu tố format từ cùng một Schema. Bên gọi không duy trì bộ template thứ hai; field
// thay đổi cũng không làm prompt và response_format rẽ nhánh.
func PreparePrompt(base string, c Contract, res Resolution) (string, error) {
	if res.Mode != ModePromptContract {
		return base, nil
	}
	schemaJSON, err := json.Marshal(c.Schema)
	if err != nil {
		return "", fmt.Errorf("llmcontract: marshal %s prompt schema: %w", c.Name, err)
	}
	contract := "## Contract đầu ra\n\n" +
		"Chỉ xuất một object JSON phù hợp JSON Schema dưới đây, không xuất giải thích, Markdown fence hoặc chính thẻ.\n\n" +
		"<output-json-schema>\n" + string(schemaJSON) + "\n</output-json-schema>"
	if strings.TrimSpace(base) == "" {
		return contract, nil
	}
	return strings.TrimSpace(base) + "\n\n" + contract, nil
}

// Nullable mở rộng type của một schema thành union nullable (["<t>","null"]), dùng trong strict
// mode để biểu đạt "mọi field required, ngữ nghĩa tùy chọn dùng null". Trả bản sao, không sửa map đầu vào.
func Nullable(s map[string]any) map[string]any {
	out := maps.Clone(s)
	if t, ok := out["type"].(string); ok {
		out["type"] = []string{t, "null"}
	}
	switch values := out["enum"].(type) {
	case []string:
		enum := make([]any, 0, len(values)+1)
		for _, value := range values {
			enum = append(enum, value)
		}
		out["enum"] = append(enum, nil)
	case []any:
		enum := slices.Clone(values)
		for _, value := range enum {
			if value == nil {
				return out
			}
		}
		out["enum"] = append(enum, nil)
	}
	return out
}

// ValidateStrictReady kiểm tra đệ quy schema thỏa tiền đề cấu trúc của subset strict OpenAI:
// mọi property của object phải được liệt kê trong required (ngữ nghĩa tùy chọn biểu đạt bằng union null). litellm
// làm kiểm tra tương tự ở lúc request và tự bổ sung additionalProperties:false; kiểm thử contract dùng hàm này
// để assert trước (RFC §11.1), không để vấn đề cấu trúc tới runtime.
func ValidateStrictReady(s map[string]any) error {
	return validateStrictReady(s, "$")
}

func validateStrictReady(s map[string]any, path string) error {
	if typeIncludes(s["type"], "object") {
		props, _ := s["properties"].(map[string]any)
		required, _ := s["required"].([]string)
		for name, sub := range props {
			if !slices.Contains(required, name) {
				return fmt.Errorf("%s.%s chưa được đưa vào required(strict yêu cầu mọi thuộc tính là required)", path, name)
			}
			if subMap, ok := sub.(map[string]any); ok {
				if err := validateStrictReady(subMap, path+"."+name); err != nil {
					return err
				}
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		return validateStrictReady(items, path+"[]")
	}
	return nil
}

func typeIncludes(t any, want string) bool {
	switch v := t.(type) {
	case string:
		return v == want
	case []string:
		return slices.Contains(v, want)
	}
	return false
}

// Fingerprint trả 12 ký tự hex đầu của sha256 JSON schema chuẩn hóa, dùng liên kết log;
// encoding/json sắp xếp key map, cùng một contract tự nhiên ổn định.
func (c Contract) Fingerprint() string {
	data, err := json.Marshal(c.Schema)
	if err != nil {
		return "unmarshalable"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}
