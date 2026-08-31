package models

import "strings"

// SameModelID kiểm tra hai định danh model có trỏ tới cùng model chuẩn không, bỏ qua hậu tố ngày, hoa/thường và khác biệt dấu chấm/dấu gạch.
func SameModelID(a, b string) bool {
	return modelLookupMatches(normalizeModelLookupID(a), normalizeModelLookupID(b))
}

func lookupModelEntry(models []ModelEntry, providerName, modelID string) (ModelEntry, bool) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	targetID := normalizeModelLookupID(modelID)
	for _, m := range models {
		if providerName != "" && !strings.EqualFold(m.Provider, providerName) {
			continue
		}
		if modelLookupMatches(normalizeModelLookupID(m.ID), targetID) {
			return m, true
		}
	}
	return ModelEntry{}, false
}

func normalizeModelLookupID(modelID string) string {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	return strings.ReplaceAll(modelID, ".", "-")
}

// modelLookupMatches khớp chính xác hoặc khớp khi có hậu tố ngày.
// e.g. "claude-sonnet-4" khớp "claude-sonnet-4-20250514".
func modelLookupMatches(knownID, targetID string) bool {
	if knownID == targetID {
		return true
	}
	if strings.HasPrefix(targetID, knownID) && isDatedModelSuffix(targetID[len(knownID):]) {
		return true
	}
	if strings.HasPrefix(knownID, targetID) && isDatedModelSuffix(knownID[len(targetID):]) {
		return true
	}
	return false
}

// isDatedModelSuffix kiểm tra chuỗi có dạng "-20250514" hay không, gồm dấu gạch nối và 8 chữ số.
func isDatedModelSuffix(s string) bool {
	if len(s) != 9 || s[0] != '-' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func hasDatedSuffix(id string) bool {
	if len(id) < 9 {
		return false
	}
	return isDatedModelSuffix(id[len(id)-9:])
}
