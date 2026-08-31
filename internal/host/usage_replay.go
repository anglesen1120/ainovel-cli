package host

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/voocel/agentcore"
)

// sessionRecord là dạng phân tích gọn nhẹ của một bản ghi trong meta/sessions/*.jsonl — chỉ lấy
// các trường cần thiết để cộng dồn usage. Bỏ qua việc phân tích các trường lớn như Content để tiết kiệm IO khi khởi động.
//
// Ba mức hạ cấp để xác định model:
//  1. Usage.Provider/Model — model phản hồi thực tế được agentcore/litellm truyền thẳng qua (ưu tiên)
//  2. Meta(_meta)          — khi thượng nguồn không truyền qua, phía ghi sẽ điền model "có hiệu lực lúc đó" bằng ModelLookup
//  3. Không có gì          — replay quay về effectiveModel để suy ngược bằng ModelSet hiện tại (độ chính xác giảm)
type sessionRecord struct {
	Role  agentcore.Role     `json:"role"`
	Usage *agentcore.Usage   `json:"usage,omitempty"`
	Meta  *sessionRecordMeta `json:"_meta,omitempty"`
}

type sessionRecordMeta struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// ReplaySessions quét meta/sessions/agents/*.jsonl,
// cộng dồn lại usage của từng tin nhắn assistant vào tracker. Trả về số bản ghi đã bù lại.
//
// Ràng buộc khi gọi: chỉ gọi một lần để bù dữ liệu khi thiếu meta/usage.json.
// Việc lưu bền vững hằng ngày đi qua SaveNow / autoSaveLoop.
//
// Độ chính xác phụ thuộc vào ba mức hạ cấp đã nêu trong chú thích của sessionRecord — mức 3 (thiếu cả Usage và _meta)
// chỉ được kích hoạt với log rất cũ hoặc khi thượng nguồn bất thường.
func (t *UsageTracker) ReplaySessions(rootDir string) (int, error) {
	if t == nil {
		return 0, nil
	}
	sessionsDir := filepath.Join(rootDir, "meta", "sessions")
	info, err := os.Stat(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.IsDir() {
		return 0, nil
	}

	total := 0
	agentsDir := filepath.Join(sessionsDir, "agents")
	walkErr := filepath.WalkDir(agentsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		agentName := parseAgentNameFromFile(name)
		if agentName == "" {
			return nil
		}
		n, fileErr := t.replayFile(path, agentName)
		if fileErr != nil {
			slog.Warn("replay agent session failed", "module", "usage", "file", name, "err", fileErr)
			return nil
		}
		total += n
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		return total, walkErr
	}
	return total, nil
}

// replayFile quét một file jsonl đơn lẻ, đưa mọi tin nhắn assistant có Usage vào accumulate.
// agentName được bên gọi phân tích từ tên file phiên Worker.
func (t *UsageTracker) replayFile(path, agentName string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	role := agentRoleName(agentName)
	count := 0
	scanner := bufio.NewScanner(f)
	// Một dòng có thể rất dài (tin nhắn assistant + tool args, v.v. đều đã được làm phẳng), nới giới hạn lên 4MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec sessionRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Role != agentcore.RoleAssistant || rec.Usage == nil {
			continue
		}
		provider, modelName := usageActualModel(rec.Usage)
		if rec.Meta != nil {
			if provider == "" {
				provider = rec.Meta.Provider
			}
			if modelName == "" {
				modelName = rec.Meta.Model
			}
		}
		t.accumulate(role, provider, modelName, *rec.Usage)
		count++
	}
	if err := scanner.Err(); err != nil {
		return count, fmt.Errorf("scan %s: %w", path, err)
	}
	return count, nil
}

// parseAgentNameFromFile trích xuất tên agent từ "writer-ch01.jsonl" / "architect_short-001.jsonl"
// (phần trước "-"). Quy ước đặt tên xem tại store/session.go::subAgentPath:
// agentName không chứa dash, suffix là ch<n> hoặc số thứ tự tăng dần.
func parseAgentNameFromFile(name string) string {
	base := strings.TrimSuffix(name, ".jsonl")
	if i := strings.Index(base, "-"); i > 0 {
		return base[:i]
	}
	return ""
}
