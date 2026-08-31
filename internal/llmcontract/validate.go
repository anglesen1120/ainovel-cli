package llmcontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
)

// ValidateJSON kiểm tra JSON thô có thỏa subset JSON Schema dùng cho contract trả trực tiếp hay không.
// Subset này bao phủ object/array/string/integer/number/boolean/null, required, enum và
// additionalProperties. Khi chưa khai báo additionalProperties thì tuân theo ngữ nghĩa mặc định của JSON Schema,
// không từ chối thêm các field chưa biết.
func ValidateJSON(schema map[string]any, raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("Phân tích JSON thất bại: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON có giá trị thừa phía sau")
		}
		return fmt.Errorf("Đuôi JSON không hợp lệ: %w", err)
	}
	return validateValue(schema, value, "$")
}

func validateValue(schema map[string]any, value any, path string) error {
	types, err := schemaTypes(schema["type"])
	if err != nil {
		return fmt.Errorf("%s Hợp đồng không hợp lệ: %w", path, err)
	}
	if value == nil && !slices.Contains(types, "null") {
		return fmt.Errorf("%s phải là %s，thực tế là null", path, joinTypes(types))
	} else if value != nil {
		actual := valueType(value)
		if len(types) > 0 && !slices.Contains(types, actual) && !(actual == "integer" && slices.Contains(types, "number")) {
			return fmt.Errorf("%s phải là %s，thực tế là %s", path, joinTypes(types), actual)
		}
	}

	if rawEnum, exists := schema["enum"]; exists {
		enum, err := enumValues(rawEnum)
		if err != nil {
			return fmt.Errorf("%s.enum Hợp đồng không hợp lệ: %w", path, err)
		}
		if !enumContains(enum, value) {
			return fmt.Errorf("%s phải là một trong %v, thực tế là %v", path, enum, value)
		}
	}
	if value == nil {
		return nil
	}

	switch typed := value.(type) {
	case map[string]any:
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s Hợp đồng thiếu properties", path)
		}
		required, err := requiredNames(schema["required"])
		if err != nil {
			return fmt.Errorf("%s.required Hợp đồng không hợp lệ: %w", path, err)
		}
		for _, name := range required {
			if _, exists := typed[name]; !exists {
				return fmt.Errorf("%s.%s là field bắt buộc", path, name)
			}
		}
		if allowAdditional, declared := schema["additionalProperties"].(bool); declared && !allowAdditional {
			for name := range typed {
				if _, exists := properties[name]; !exists {
					return fmt.Errorf("%s.%s chưa được khai báo trong hợp đồng", path, name)
				}
			}
		}
		for name, child := range typed {
			childSchema, exists := properties[name]
			if !exists {
				continue
			}
			childMap, ok := childSchema.(map[string]any)
			if !ok {
				return fmt.Errorf("contract %s.%s không phải object", path, name)
			}
			if err := validateValue(childMap, child, path+"."+name); err != nil {
				return err
			}
		}
	case []any:
		itemSchema, ok := schema["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s Hợp đồng thiếu items", path)
		}
		for i, item := range typed {
			if err := validateValue(itemSchema, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func schemaTypes(value any) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{typed}, nil
	case []string:
		return typed, nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("union type chỉ được chứa string")
			}
			out = append(out, text)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("type phải là string hoặc array string")
	}
}

func requiredNames(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	if names, ok := stringSlice(value); ok {
		return names, nil
	}
	return nil, fmt.Errorf("phải là array string")
}

func stringSlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, false
	}
}

func enumValues(value any) ([]any, error) {
	switch typed := value.(type) {
	case []string:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = item
		}
		return out, nil
	case []any:
		for _, item := range typed {
			if item == nil {
				continue
			}
			if _, ok := item.(string); !ok {
				return nil, fmt.Errorf("chỉ hỗ trợ chuỗi và null")
			}
		}
		return typed, nil
	default:
		return nil, fmt.Errorf("phải là array")
	}
}

func enumContains(enum []any, value any) bool {
	for _, item := range enum {
		if item == nil && value == nil {
			return true
		}
		itemText, itemOK := item.(string)
		valueText, valueOK := value.(string)
		if itemOK && valueOK && itemText == valueText {
			return true
		}
	}
	return false
}

func valueType(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		if number, err := strconv.ParseFloat(string(typed), 64); err == nil && !math.IsInf(number, 0) && math.Trunc(number) == number {
			return "integer"
		}
		return "number"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func joinTypes(types []string) string {
	if len(types) == 0 {
		return "giá trị JSON hợp lệ"
	}
	return fmt.Sprint(types)
}
