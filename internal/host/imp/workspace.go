package imp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// workspaceSchemaVersion là phiên bản schema tổng thể của workspace nhập.
// Khi không khớp, yêu cầu rõ ràng tiếp tục bằng phiên bản khớp hoặc nhập lại, không đoán cách migration (RFC §6.1).
const workspaceSchemaVersion = 1

// Digest tính digest nội dung, dùng tiếp quy ước sẵn có của repo "sha256:"+hex (xem store/checkpoints.go).
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Artifact là danh tính thống nhất của mỗi hiện vật ngữ nghĩa trong workspace: phiên bản schema + digest đầu vào + payload.
// Chỉ được tái sử dụng khi có thể tái tạo cùng InputDigest từ đầu vào ngữ nghĩa thực hiện tại (RFC §6.3 / bất biến 1).
// Không triển khai đồ thị phụ thuộc: LoadState so sánh InputDigest từng bước theo pipeline tuyến tính cố định để quyết định tái sử dụng và vô hiệu hóa, NextAction từ đó suy ra bước tiếp theo.
type Artifact[T any] struct {
	SchemaVersion int    `json:"schema_version"`
	InputDigest   string `json:"input_digest"`
	Payload       T      `json:"payload"`
}

// Manifest tương ứng với snapshot nguồn đã chuẩn hóa duy nhất, là danh tính workspace chứ không phải hiện vật phái sinh (RFC §6.1).
// Không lưu đường dẫn nguồn tuyệt đối, tránh rò rỉ thư mục máy và loại bỏ vấn đề khôi phục do di chuyển file.
type Manifest struct {
	Version          int    `json:"version"`
	SourceName       string `json:"source_name"`
	RawSHA256        string `json:"raw_sha256"`
	NormalizedSHA256 string `json:"normalized_sha256"`
	Encoding         string `json:"encoding"`
	SizeBytes        int64  `json:"size_bytes"`
	CreatedAt        string `json:"created_at"`
}

// Intent lưu ủy quyền rõ ràng của người dùng khi bắt đầu nhập; sau khi khôi phục vẫn phải tuân thủ, không suy ra từ hiện vật, Runner không âm thầm viết lại (RFC §6.1).
type Intent struct {
	Version             int    `json:"version"`
	AutoConfirm         bool   `json:"auto_confirm,omitempty"`
	StoryResolution     string `json:"story_resolution,omitempty"` // open / closed
	ContinueAfterImport bool   `json:"continue_after_import,omitempty"`
}

// Đường dẫn tương đối của các hiện vật chuẩn trong workspace.
const (
	fileManifest     = "manifest.json"
	fileIntent       = "intent.json"
	fileSource       = "source.txt"
	fileGuidance     = "guidance.txt"
	fileSegmentation = "segmentation.json"
	fileConfirmation = "confirmation.json"
	fileSynthesis    = "synthesis.json"
	fileStoryResolve = "story-resolution.json"
	dirAnalyses      = "analyses"
	dirRangeDigests  = "range-digests"
	dirSegmentChunks = "segment-chunks"
	dirFailures      = "failures"
)

// Workspace là handle đọc/ghi hiện vật nguyên tử cho thư mục <thu_muc_sach>/meta/import/.
type Workspace struct {
	dir string
}

// OpenWorkspace trả về handle trỏ tới meta/import/ dưới thư mục gốc sách; không đảm bảo thư mục đã tồn tại, dùng Active() để kiểm tra.
func OpenWorkspace(bookDir string) *Workspace {
	return &Workspace{dir: filepath.Join(bookDir, "meta", "import")}
}

// Dir trả về đường dẫn tuyệt đối của workspace (dùng cho chẩn đoán và vị trí ghi hiện vật lỗi).
func (w *Workspace) Dir() string { return w.dir }

func (w *Workspace) path(rel string) string { return filepath.Join(w.dir, rel) }

// Active kiểm tra có workspace hoạt động đã được publish hay không. Nếu meta/import/ không tồn tại thì không tính là hoạt động,
// thư mục khởi tạo dở tồn tại dưới dạng meta/import.init-* sẽ không bị nhận nhầm là hoạt động (RFC §6.1).
func (w *Workspace) Active() bool {
	fi, err := os.Stat(w.dir)
	return err == nil && fi.IsDir()
}

func (w *Workspace) has(rel string) bool {
	_, err := os.Stat(w.path(rel))
	return err == nil
}

// writeAtomic ghi nguyên tử rel (tương đối với workspace) bằng "file tạm + fsync + rename".
func (w *Workspace) writeAtomic(rel string, data []byte) error {
	full := w.path(rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), filepath.Base(full)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, full); err != nil {
		return err
	}
	syncDir(filepath.Dir(full))
	return nil
}

// syncDir fsync mục thư mục theo best-effort, để rename vừa hoàn tất vẫn bền vững sau khi mất điện.
// Các nền tảng như Windows có thể không hỗ trợ Sync trên thư mục; bỏ qua lỗi đó — an toàn khi tiến trình crash không phụ thuộc vào nó, chỉ bổ sung cho tình huống mất điện (RFC §12.3).
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

func (w *Workspace) writeJSON(rel string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return w.writeAtomic(rel, append(data, '\n'))
}

func (w *Workspace) readJSON(rel string, v any) error {
	data, err := os.ReadFile(w.path(rel))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// LoadManifest đọc danh tính snapshot nguồn của workspace.
func (w *Workspace) LoadManifest() (*Manifest, error) {
	var m Manifest
	if err := w.readJSON(fileManifest, &m); err != nil {
		return nil, err
	}
	if m.Version != workspaceSchemaVersion {
		return nil, fmt.Errorf("phiên bản schema manifest %d != %d, vui lòng dùng phiên bản khớp để tiếp tục hoặc nhập lại", m.Version, workspaceSchemaVersion)
	}
	return &m, nil
}

// LoadIntent đọc ủy quyền khởi động của người dùng.
func (w *Workspace) LoadIntent() (*Intent, error) {
	var in Intent
	if err := w.readJSON(fileIntent, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// LoadSource đọc văn bản snapshot nguồn đã chuẩn hóa.
func (w *Workspace) LoadSource() ([]byte, error) {
	return os.ReadFile(w.path(fileSource))
}

// LoadGuidance đọc hướng dẫn chia đoạn của người dùng (RFC §18.3); thiếu nghĩa là không có hướng dẫn.
// Hướng dẫn và source.txt đều là đầu vào ngữ nghĩa của việc chia đoạn chứ không phải hiện vật phái sinh, được cập nhật rõ ràng bằng --guide,
// thay đổi nội dung sẽ tự nhiên làm segmentation và InputDigest downstream của nó không khớp.
func (w *Workspace) LoadGuidance() (string, error) {
	data, err := os.ReadFile(w.path(fileGuidance))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readBytes đọc byte gốc của hiện vật, dùng để ràng buộc InputDigest downstream.
func (w *Workspace) readBytes(rel string) ([]byte, error) {
	return os.ReadFile(w.path(rel))
}

// writeArtifact ghi hiện vật ngữ nghĩa có danh tính thống nhất.
func writeArtifact[T any](w *Workspace, rel, inputDigest string, payload T) error {
	return w.writeJSON(rel, Artifact[T]{
		SchemaVersion: workspaceSchemaVersion,
		InputDigest:   inputDigest,
		Payload:       payload,
	})
}

// readArtifact đọc hiện vật ngữ nghĩa và kiểm tra phiên bản schema; việc InputDigest có khớp hay không do caller quyết định theo đầu vào hiện tại.
func readArtifact[T any](w *Workspace, rel string) (*Artifact[T], error) {
	var a Artifact[T]
	if err := w.readJSON(rel, &a); err != nil {
		return nil, err
	}
	if a.SchemaVersion != workspaceSchemaVersion {
		return nil, fmt.Errorf("phiên bản schema %s %d != %d, vui lòng dùng phiên bản khớp để tiếp tục hoặc nhập lại", rel, a.SchemaVersion, workspaceSchemaVersion)
	}
	return &a, nil
}

// clearDir xóa một thư mục cache trung gian trong workspace. Lỗi phải giao cho caller xử lý: nuốt lỗi sẽ khiến
// thông báo "đã xóa" nói dối — lần chạy lại sau vẫn tái sử dụng cache hỏng (trình chống virus/handle bị chiếm dụng trên Windows là tình huống thực tế, Debug-First).
func (w *Workspace) clearDir(rel string) error {
	return os.RemoveAll(w.path(rel))
}

// FailureMeta là metadata chẩn đoán của lần thất bại gần nhất (RFC §14.2).
type FailureMeta struct {
	Stage         string `json:"stage"`
	Detail        string `json:"detail"`
	StopReason    string `json:"stop_reason,omitempty"`
	PrefixSalvage string `json:"prefix_salvage,omitempty"` // available:N / unavailable
}

// writeFailure lưu metadata thất bại gần nhất và phản hồi model gốc chưa cắt vào failures/ theo best-effort (RFC §14.2).
// Phản hồi gốc có thể chứa nội dung chính văn, chỉ nằm trong thư mục sách của chính người dùng, không đi vào log thông thường hoặc bản xuất chẩn đoán đã khử định danh.
func (w *Workspace) writeFailure(meta FailureMeta, rawResponse string) {
	_ = w.writeJSON(filepath.Join(dirFailures, "last.json"), meta)
	_ = w.writeAtomic(filepath.Join(dirFailures, "last-response.txt"), []byte(rawResponse))
}

// createWorkspace ghi đủ manifest/intent/source vào thư mục tạm và kiểm tra, rồi publish nguyên tử thành meta/import/ bằng rename thư mục.
// Nhờ vậy bộ ba ban đầu sẽ không đi vào NextAction ở trạng thái khởi tạo dở, và cũng không cần stage=initializing (RFC §6.1).
func createWorkspace(bookDir string, m Manifest, in Intent, normalized []byte) (*Workspace, error) {
	base := filepath.Join(bookDir, "meta")
	final := filepath.Join(base, "import")
	if fi, err := os.Stat(final); err == nil && fi.IsDir() {
		return nil, fmt.Errorf("workspace nhập đã tồn tại: %s (/import không tham số có thể khôi phục từ đó)", final)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(base, "import.init-*")
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(tmp)
		}
	}()

	tw := &Workspace{dir: tmp}
	if err := tw.writeAtomic(fileSource, normalized); err != nil {
		return nil, err
	}
	if err := tw.writeJSON(fileManifest, m); err != nil {
		return nil, err
	}
	if err := tw.writeJSON(fileIntent, in); err != nil {
		return nil, err
	}
	// Trước khi publish, kiểm tra bộ ba có thể đọc được và snapshot nguồn khớp với manifest, ngăn chặn workspace ghi dở.
	got, err := tw.LoadManifest()
	if err != nil {
		return nil, fmt.Errorf("kiểm tra manifest ban đầu: %w", err)
	}
	src, err := tw.LoadSource()
	if err != nil {
		return nil, fmt.Errorf("kiểm tra snapshot nguồn ban đầu: %w", err)
	}
	if d := Digest(src); d != got.NormalizedSHA256 {
		return nil, fmt.Errorf("digest snapshot nguồn ban đầu không nhất quán: %s != %s", d, got.NormalizedSHA256)
	}
	if _, err := tw.LoadIntent(); err != nil {
		return nil, fmt.Errorf("kiểm tra intent ban đầu: %w", err)
	}

	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	syncDir(base)
	committed = true
	return &Workspace{dir: final}, nil
}
