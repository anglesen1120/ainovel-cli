package host

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/models"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// recentSampleCap là kích thước cửa sổ trượt: chỉ giữ lại N lần gọi gần nhất của mỗi role
// làm mẫu (cacheRead, input), dùng ở cột bên trái để so sánh tỷ lệ hit "tích lũy vs gần N lần",
// nhằm nhận diện "giai đoạn đầu kéo tụt" và "hit thấp ở trạng thái ổn định".
const recentSampleCap = 10

// Ngưỡng kép để phán định đứt chuỗi cache (căn theo kinh nghiệm thực nghiệm của Claude Code):
// lượng hit giảm hơn 5% (tương đối) so với lần trước và mức giảm >=2000 tokens (tuyệt đối) mới
// được tính là đứt chuỗi -- chỉ dùng ngưỡng tương đối sẽ bị nhiễu tiền tố nhỏ che lấp, chỉ dùng
// ngưỡng tuyệt đối sẽ bỏ sót suy giảm đáng kể của tiền tố lớn.
const (
	cacheBreakKeepRatio     = 0.95
	cacheBreakMinDropTokens = 2000
)

// UsageTracker cộng dồn token đầu vào/đầu ra LLM và chi phí USD của tất cả agent trong toàn bộ phiên.
//
// Cơ chế hoạt động:
//   - Mỗi lần callback OnMessage của agent được kích hoạt thì gọi Record(agentName, msg)
//   - agentName được ánh xạ sang role (architect_* được chuẩn hóa thành architect), tra model đang được gán cho role đó trong ModelSet hiện tại
//   - Dùng models.DefaultRegistry tra giá model, nhân cộng theo bốn mục: đầu vào không cache/đầu ra/cache read/cache write
//   - Khi registry không có model này, quay về msg.Usage.Cost.Total (provider tự mang theo, có thể là 0)
//   - Sau khi đổi nóng model (/model), các message tiếp theo tự động tính giá theo model mới, message cũ giữ nguyên chi phí cũ
//
// Đồng thời duy trì theo chiều per-role (writer/editor/architect):
//   - Dữ liệu hit tích lũy -> hiệu quả tối ưu tổng thể
//   - Cửa sổ trượt gần N lần -> phân biệt giai đoạn đầu kéo tụt vs hit thấp ở trạng thái ổn định
//   - Cờ CacheCapable -> phân biệt "chưa bật" và "thực sự hit 0%"
//
// An toàn luồng.
type UsageTracker struct {
	mu       sync.Mutex
	overall  agentTotals
	perAgent map[string]*agentTotals // key là tên role sau khi agentRoleName chuẩn hóa
	perModel map[string]*agentTotals // key là provider/model; khi không biết provider thì thoái hóa thành model
	modelSet *bootstrap.ModelSet
	store    *storepkg.Store // có thể là nil (ngữ cảnh test), khi nil thì mọi phương thức persistence lặng lẽ noop

	// cacheTrack là baseline chuỗi cache theo per-role (độ dài tiền tố/lượng hit/thời gian của lần gọi trước),
	// dùng để phát hiện đứt chuỗi. Chỉ cập nhật trên đường live Record -- replay lịch sử không phát hiện,
	// nếu không mỗi lần khởi động đều biến đứt chuỗi cũ thành cảnh báo nhầm. Không persistence.
	cacheTrack map[string]*cacheTrackState

	// missingAssistantUsage cộng dồn số lần "nhận được message assistant nhưng Usage là nil".
	// Qua thực tế, tình huống này chủ yếu xảy ra khi backend tự dựng tương thích OpenAI không gửi final usage chunk
	// ở cuối streaming theo giao thức OpenAI stream_options.include_usage -- partial.Usage luôn là nil,
	// tất cả trường cộng dồn đều kẹt ở 0. Counter cho phép UI báo thẳng với người dùng rằng
	// "upstream không trả usage, không phải bên này hỏng", thay vì cứ bám vào code panel cache.
	missingAssistantUsage int
	loggedMissingUsage    bool // chỉ warn một lần trong toàn bộ phiên, tránh tui.log bị spam

	// saveCh do Record kích hoạt không chặn sau khi cộng dồn; autoSaveLoop lắng nghe và ghi xuống đĩa theo debounce.
	// buffered=1: nhiều lần Record liên tiếp được gộp thành một tín hiệu ghi; đầy thì bỏ, tick sau sẽ ghi chung.
	saveCh       chan struct{}
	autoSaveMu   sync.Mutex
	autoSaveDone chan struct{}

	// onCost được gọi ngoài khóa sau mỗi lần ghi sổ, kèm chi phí tích lũy mới nhất (BudgetSentinel kiểm tra vượt ngưỡng).
	// Phải được đặt bằng SetOnCost trước khi các Record concurrent bắt đầu, sau đó chỉ đọc.
	onCost func(total float64)

	// onMissingUsage được gọi một lần khi lần đầu phát hiện "message assistant không có Usage" (cùng thời điểm với slog warn).
	// Khi bật budget, điều này nghĩa là có vùng mù tính phí -- chi phí luôn 0, budget không bao giờ kích hoạt, bắt buộc phải báo người.
	onMissingUsage func()
}

// usageSample là mẫu hit của một lần OnMessage, chỉ ghi tử số và mẫu số của tỷ lệ hit.
type usageSample struct {
	CacheRead int
	Input     int
}

// cacheTrackState là baseline chuỗi cache của một role trong phiên hiện tại. task (văn bản task spawn) là
// định danh phiên: đổi task = spawn mới = huyết thống cache mới (prompt_cache_key có #seq), request đầu tiên
// hit thấp là bình thường, đổi thẳng baseline mà không so sánh -- nếu không, khi "phiên trước rất ngắn,
// request đầu của phiên mới lại có tiền tố dài hơn" sẽ cảnh báo đứt chuỗi nhầm. Ngữ nghĩa Input
// (bao gồm CacheRead, xem chú thích computeCost) vừa đúng bằng "độ dài tiền tố server xử lý",
// nhờ đó phân biệt ba xu hướng: tiền tố ngắn lại = nén context trong phiên (hợp lệ, reset baseline);
// tiền tố tăng và hit cũng tăng = chuỗi khỏe; tiền tố tăng nhưng hit tụt mạnh = đứt chuỗi.
type cacheTrackState struct {
	task          string
	lastPrefix    int
	lastCacheRead int
	lastAt        time.Time
}

// agentTotals là bộ đếm tích lũy của một agent.
//   - Saved là khoản chênh lệch tính ngược từ dữ liệu hit hiện tại theo giả định "nếu tính giá như đầu vào không cache"
//   - CacheCapable chỉ được đặt true sau khi role đó có ít nhất một lần gọi "model đã biết hỗ trợ cache"
//   - samples là ring buffer độ dài cố định; recentSampleCap lần đầu append trực tiếp, sau đó xoay vòng theo sampleIdx
type agentTotals struct {
	Input        int
	Output       int
	CacheRead    int
	CacheWrite   int
	Cost         float64
	Saved        float64
	CacheCapable bool
	CacheBreaks  int // số lần đứt chuỗi cache phát hiện khi live (replay không tính)
	samples      []usageSample
	sampleIdx    int
}

func NewUsageTracker(set *bootstrap.ModelSet, store *storepkg.Store) *UsageTracker {
	return &UsageTracker{
		modelSet:   set,
		store:      store,
		perAgent:   make(map[string]*agentTotals, 4),
		perModel:   make(map[string]*agentTotals, 4),
		cacheTrack: make(map[string]*cacheTrackState, 4),
		saveCh:     make(chan struct{}, 1),
	}
}

// Record phân phối một message agent vào hai đường: cộng dồn / chẩn đoán.
//
// Cộng dồn chỉ nhìn Usage có tồn tại hay không -- "message nào mang Usage" là chi tiết lắp ráp
// của agentcore/litellm adapter (giao thức upstream đặt usage ở tầng trên cùng của response),
// sau này quy tắc lắp ráp đổi cũng không cần sửa ở đây. Chẩn đoán yêu cầu Role=Assistant và
// Content không rỗng, tránh AbortMsg / khôi phục bất thường / tool / message user làm nhiễu
// bộ đếm missingAssistantUsage.
func (t *UsageTracker) Record(agentName, task string, msg agentcore.AgentMessage) {
	if t == nil {
		return
	}
	m, ok := msg.(agentcore.Message)
	if !ok {
		return
	}
	if m.Usage == nil {
		if m.Role == agentcore.RoleAssistant && len(m.Content) > 0 {
			t.flagMissingUsage(agentName)
		}
		return
	}
	role := agentRoleName(agentName)
	t.noteCacheBreak(role, task, *m.Usage)
	provider, modelName := usageActualModel(m.Usage)
	t.accumulate(role, provider, modelName, *m.Usage)
}

// noteCacheBreak là phát hiện đứt chuỗi cache (chỉ quan sát, không sửa chữa, chỉ gọi trên đường live Record).
//
// Phán định: trong cùng một phiên (role+task), tiền tố (Input, gồm CacheRead) không ngắn lại, còn lượng hit
// giảm >5% so với lần trước và mức giảm >=2000 tokens. task thay đổi = spawn mới = huyết thống cache mới,
// đổi thẳng baseline không so sánh; tiền tố ngắn lại nghĩa là nén context, thuộc mức giảm hợp lệ, chỉ reset
// baseline không cảnh báo. Quy nguyên nhân theo độ ưu tiên để đưa gợi ý: khoảng cách vượt TTL -> nghi hết hạn;
// khoảng cách rất ngắn và byte client đáng lẽ ổn định -> nghi server eviction / lệch route
// (trạm trung chuyển polling upstream là nguyên nhân thường gặp).
func (t *UsageTracker) noteCacheBreak(role, task string, u agentcore.Usage) {
	now := time.Now()
	prefix := u.Input // litellm đảm bảo Input gồm CacheRead ở mọi provider

	t.mu.Lock()
	st := t.cacheTrack[role]
	if st == nil || st.task != task {
		t.cacheTrack[role] = &cacheTrackState{task: task, lastPrefix: prefix, lastCacheRead: u.CacheRead, lastAt: now}
		t.mu.Unlock()
		return
	}
	prevPrefix, prevRead, prevAt := st.lastPrefix, st.lastCacheRead, st.lastAt
	st.lastPrefix, st.lastCacheRead, st.lastAt = prefix, u.CacheRead, now

	broke := prevPrefix > 0 && prefix >= prevPrefix &&
		float64(u.CacheRead) < float64(prevRead)*cacheBreakKeepRatio &&
		prevRead-u.CacheRead >= cacheBreakMinDropTokens
	if broke {
		t.overall.CacheBreaks++
		per := t.perAgent[role]
		if per == nil {
			per = &agentTotals{}
			t.perAgent[role] = per
		}
		per.CacheBreaks++
	}
	t.mu.Unlock()

	if !broke {
		return
	}
	gap := now.Sub(prevAt).Round(time.Second)
	hint := "nghi server eviction / lệch route (trạm trung chuyển polling upstream là nguyên nhân thường gặp)"
	if gap > time.Hour {
		hint = "nghi TTL 1h đã hết hạn"
	} else if gap > 5*time.Minute {
		hint = "nghi TTL 5m đã hết hạn"
	}
	slog.Warn("Đứt chuỗi cache: tiền tố không ngắn lại nhưng hit tụt mạnh",
		"module", "usage", "role", role,
		"cache_read", fmt.Sprintf("%d→%d", prevRead, u.CacheRead),
		"prefix", fmt.Sprintf("%d→%d", prevPrefix, prefix),
		"gap", gap.String(), "hint", hint)
	t.notifyDirty()
}

func usageActualModel(u *agentcore.Usage) (provider, modelName string) {
	if u == nil {
		return "", ""
	}
	return strings.TrimSpace(u.Provider), strings.TrimSpace(u.Model)
}

// flagMissingUsage cộng dồn một sự kiện "trông giống response LLM thật nhưng không lấy được usage",
// và chỉ ghi một log warn trong toàn phiên để tránh tui.log bị spam.
func (t *UsageTracker) flagMissingUsage(agentName string) {
	t.mu.Lock()
	t.missingAssistantUsage++
	shouldLog := !t.loggedMissingUsage
	t.loggedMissingUsage = true
	t.mu.Unlock()
	if shouldLog {
		slog.Warn("Response LLM không mang dữ liệu usage, panel cache/chi phí sẽ không có số cộng dồn -- thường là upstream streaming không gửi final usage chunk theo giao thức OpenAI include_usage",
			"module", "usage", "agent", agentName)
		if t.onMissingUsage != nil {
			t.onMissingUsage()
		}
	}
	t.notifyDirty()
}

// SetOnMissingUsage đăng ký callback một lần cho "lần đầu phát hiện thiếu usage".
// Phải gọi một lần trong giai đoạn dựng Host, trước khi các Record concurrent bắt đầu.
func (t *UsageTracker) SetOnMissingUsage(cb func()) {
	if t == nil {
		return
	}
	t.onMissingUsage = cb
}

// notifyDirty kích hoạt không chặn một tín hiệu ghi xuống đĩa, do autoSaveLoop thực sự ghi theo debounce.
// Kênh tín hiệu buffered=1: nhiều lần Record liên tiếp chỉ cần gộp thành một yêu cầu lưu.
func (t *UsageTracker) notifyDirty() {
	if t == nil || t.saveCh == nil {
		return
	}
	select {
	case t.saveCh <- struct{}{}:
	default:
	}
}

// accumulate cộng dồn một message có Usage vào ba bộ đếm overall / per-role / per-model.
// provider/model rỗng nghĩa là "dùng ModelSet hiện tại để lấy model tương ứng role" (đường realtime);
// không rỗng nghĩa là "ép tính giá theo model chỉ định" (đường replay dùng _meta trong session jsonl).
// resolveCost chạy ngoài khóa (nó chỉ đọc modelSet/Registry), trong khóa chỉ làm phép cộng.
func (t *UsageTracker) accumulate(role, provider, modelName string, u agentcore.Usage) {
	provider, modelName = t.effectiveModel(role, provider, modelName)
	cost, saved, capable := t.resolveCost(modelName, u)

	t.mu.Lock()
	addUsage(&t.overall, u, cost, saved, capable)

	per := t.perAgent[role]
	if per == nil {
		per = &agentTotals{}
		t.perAgent[role] = per
	}
	addUsage(per, u, cost, saved, capable)

	if key := modelUsageKey(provider, modelName); key != "" {
		perModel := t.perModel[key]
		if perModel == nil {
			perModel = &agentTotals{}
			t.perModel[key] = perModel
		}
		addUsage(perModel, u, cost, saved, capable)
	}
	total := t.overall.Cost
	t.mu.Unlock()

	t.notifyDirty()
	if t.onCost != nil {
		t.onCost(total)
	}
}

// SetOnCost đăng ký callback ghi sổ (mang theo chi phí tích lũy mới nhất, gọi ngoài khóa).
// Phải gọi một lần trong giai đoạn dựng Host, trước khi các Record concurrent bắt đầu.
func (t *UsageTracker) SetOnCost(cb func(total float64)) {
	if t == nil {
		return
	}
	t.onCost = cb
}

func (t *UsageTracker) effectiveModel(role, provider, modelName string) (string, string) {
	provider = strings.TrimSpace(provider)
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		if t != nil && t.modelSet != nil {
			p, m, _ := t.modelSet.CurrentSelection(role)
			return p, m
		}
		return "", ""
	}
	if provider == "" && t != nil && t.modelSet != nil {
		p, m, _ := t.modelSet.CurrentSelection(role)
		if m == modelName {
			provider = p
		}
	}
	return provider, modelName
}

func modelUsageKey(provider, modelName string) string {
	provider = strings.TrimSpace(provider)
	modelName = strings.TrimSpace(modelName)
	switch {
	case modelName == "":
		return ""
	case provider == "":
		return modelName
	default:
		return provider + "/" + modelName
	}
}

// addUsage cộng dồn token và chi phí của một lần gọi vào một totals.
// Phải gọi khi đang giữ UsageTracker.mu.
//
// CacheCapable ưu tiên dùng "sự thật" để phán định: chỉ cần từng thấy CacheRead hoặc CacheWrite > 0
// thì chứng minh upstream thực sự đã làm prompt caching. CacheReadCostPer1M trong registry chỉ là fallback,
// vì các model backend tự dựng (mimo-v2.5-pro / proxy nội địa, v.v.) thường không nằm trong chỉ mục pricing
// của BerriAI/litellm, nhưng trong Usage thực tế hoàn toàn có dữ liệu cache, UI không nên phán nhầm là "chưa bật".
func addUsage(t *agentTotals, u agentcore.Usage, cost, saved float64, capable bool) {
	t.Input += u.Input
	t.Output += u.Output
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
	t.Cost += cost
	t.Saved += saved
	if capable || u.CacheRead > 0 || u.CacheWrite > 0 {
		t.CacheCapable = true
	}
	pushSample(t, u.CacheRead, u.Input)
}

// pushSample đẩy một mẫu vào ring buffer. recentSampleCap lần đầu chỉ append, sau đó xoay vòng ghi đè.
func pushSample(t *agentTotals, cacheRead, input int) {
	s := usageSample{CacheRead: cacheRead, Input: input}
	if len(t.samples) < recentSampleCap {
		t.samples = append(t.samples, s)
		return
	}
	t.samples[t.sampleIdx] = s
	t.sampleIdx = (t.sampleIdx + 1) % recentSampleCap
}

// recentSums trả về tổng cacheRead và input trong cửa sổ trượt, làm tử số và mẫu số của "tỷ lệ hit gần N lần".
// Dùng sum/sum thay vì "trung bình tỷ lệ từng lần" để tránh mẫu nhỏ (input=vài trăm token) phóng đại nhiễu.
func recentSums(t *agentTotals) (cacheRead, input int) {
	for _, s := range t.samples {
		cacheRead += s.CacheRead
		input += s.Input
	}
	return cacheRead, input
}

// Totals trả về snapshot của tổng lượng tích lũy.
func (t *UsageTracker) Totals() (cost float64, input, output, cacheRead, cacheWrite int) {
	if t == nil {
		return 0, 0, 0, 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.Cost, t.overall.Input, t.overall.Output, t.overall.CacheRead, t.overall.CacheWrite
}

// SavedUSD trả về số USD tích lũy tiết kiệm được nhờ cache hit.
func (t *UsageTracker) SavedUSD() float64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.Saved
}

// OverallRecent trả về tổng cacheRead, tổng input và số mẫu trong cửa sổ trượt (<= recentSampleCap lần).
func (t *UsageTracker) OverallRecent() (cacheRead, input, samples int) {
	if t == nil {
		return 0, 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r, in := recentSums(&t.overall)
	return r, in, len(t.overall.samples)
}

// OverallCacheBreaks trả về tổng số lần đứt chuỗi cache được phát hiện khi live.
func (t *UsageTracker) OverallCacheBreaks() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.CacheBreaks
}

// OverallCacheCapable cho biết tổng thể đã từng có ít nhất một lần gọi model đã biết hỗ trợ cache hay chưa.
func (t *UsageTracker) OverallCacheCapable() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.CacheCapable
}

// MissingAssistantUsage trả về số lần tích lũy "nhận được message assistant nhưng Usage là nil".
// Lớn hơn 0 thường nghĩa là upstream streaming không gửi final usage chunk của OpenAI,
// UI dựa vào đó hiển thị gợi ý thay vì hiểu nhầm là module cache bản thân bị hỏng.
func (t *UsageTracker) MissingAssistantUsage() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.missingAssistantUsage
}

// -- Persistence --

// Snapshot sao chép trạng thái tích lũy hiện tại thành domain.UsageState có thể serialize.
// samples của cửa sổ trượt không vào snapshot -- nó là cửa sổ chẩn đoán ngắn hạn, không nhiều ý nghĩa xuyên process.
func (t *UsageTracker) Snapshot() domain.UsageState {
	if t == nil {
		return domain.UsageState{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := domain.UsageState{
		Schema:       domain.UsageSchemaVersion,
		UpdatedAt:    time.Now(),
		Overall:      totalsSnapshot(&t.overall),
		PerAgent:     make(map[string]domain.AgentUsageTotals, len(t.perAgent)),
		PerModel:     make(map[string]domain.AgentUsageTotals, len(t.perModel)),
		MissingUsage: t.missingAssistantUsage,
	}
	for role, v := range t.perAgent {
		state.PerAgent[role] = totalsSnapshot(v)
	}
	for model, v := range t.perModel {
		state.PerModel[model] = totalsSnapshot(v)
	}
	return state
}

// LoadFromStore đọc snapshot đã persistence từ store.Usage và điền ngược vào memory. Trả về true nghĩa là
// đã load thành công một trạng thái không rỗng (schema khớp); false nghĩa là không có file hoặc không dùng được,
// caller nên tiếp tục đi đường session replay để điền một lần.
func (t *UsageTracker) LoadFromStore() (bool, error) {
	if t == nil || t.store == nil {
		return false, nil
	}
	state, err := t.store.Usage.Load()
	if err != nil {
		return false, err
	}
	if state == nil {
		return false, nil
	}
	t.applyState(*state)
	return true, nil
}

// SaveNow lập tức ghi snapshot hiện tại xuống đĩa. Các đường autoSaveLoop / Close đều ghi qua nó.
func (t *UsageTracker) SaveNow() error {
	if t == nil || t.store == nil {
		return nil
	}
	return t.store.Usage.Save(t.Snapshot())
}

// StartAutoSave khởi một goroutine, lắng nghe saveCh + debounce để ghi xuống đĩa. Trước khi ctx done,
// trạng thái chưa lưu cuối cùng sẽ được flush ra. Close kích hoạt flush + thoát thông qua cancel ctx.
func (t *UsageTracker) StartAutoSave(ctx context.Context) {
	if t == nil || t.store == nil {
		return
	}
	done := make(chan struct{})
	t.autoSaveMu.Lock()
	t.autoSaveDone = done
	t.autoSaveMu.Unlock()
	go func() {
		defer close(done)
		t.autoSaveLoop(ctx)
	}()
}

// WaitAutoSave chờ lần flush cuối cùng sau khi hủy hoàn tất. Host.Close gọi cancel trước,
// rồi chờ ở đây, tránh autoSaveLoop và SaveNow trước khi thoát cùng ghi đồng thời một snapshot.
func (t *UsageTracker) WaitAutoSave() {
	if t == nil {
		return
	}
	t.autoSaveMu.Lock()
	done := t.autoSaveDone
	t.autoSaveMu.Unlock()
	if done != nil {
		<-done
	}
}

// autoSaveLoop tiết lưu tín hiệu dirty tần suất cao thành một lần ghi xuống đĩa mỗi 500ms.
//
// Ghi chú thiết kế: 500ms là giá trị kinh nghiệm -- mỗi chương có 1-2 LLM turn, ghi 1-2 lần hoàn toàn chấp nhận được;
// ngay cả khi người dùng tự ctrl+C thoát không kịp kích hoạt timer, đường hủy ctx cũng sẽ flush lần cuối.
// Crash thật sự (OS kill -9) sẽ mất phần tích lũy trong 0.5s gần nhất -- session jsonl upstream vẫn là
// sự thật đầy đủ, lần khởi động sau sẽ replay từ sessions/ để bù phần chênh.
func (t *UsageTracker) autoSaveLoop(ctx context.Context) {
	const debounce = 500 * time.Millisecond
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	var pending bool
	flush := func() {
		if err := t.SaveNow(); err != nil {
			slog.Warn("Ghi usage xuống đĩa thất bại", "module", "usage", "err", err)
		}
		pending = false
	}
	for {
		select {
		case <-ctx.Done():
			if pending {
				flush()
			}
			return
		case <-t.saveCh:
			if pending {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			timer.Reset(debounce)
			pending = true
		case <-timer.C:
			flush()
		}
	}
}

// applyState ghi snapshot đã persistence trở lại memory. Chỉ gọi khi khởi động (LoadFromStore / sau replay),
// lúc này chưa start autoSaveLoop / Record cũng sẽ không kích hoạt concurrent, nên có thể không cần khóa;
// nhưng vẫn giữ mu để phòng test hoặc thứ tự gọi trong tương lai thay đổi và đưa vào concurrency.
func (t *UsageTracker) applyState(state domain.UsageState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.overall = totalsFromState(state.Overall)
	if state.PerAgent == nil {
		t.perAgent = make(map[string]*agentTotals, 4)
	} else {
		t.perAgent = make(map[string]*agentTotals, len(state.PerAgent))
		for role, v := range state.PerAgent {
			tot := totalsFromState(v)
			t.perAgent[role] = &tot
		}
	}
	if state.PerModel == nil {
		t.perModel = make(map[string]*agentTotals, 4)
	} else {
		t.perModel = make(map[string]*agentTotals, len(state.PerModel))
		for model, v := range state.PerModel {
			tot := totalsFromState(v)
			t.perModel[model] = &tot
		}
	}
	t.missingAssistantUsage = state.MissingUsage
}

// totalsSnapshot sao chép agentTotals trong memory thành domain.AgentUsageTotals có thể persistence.
// Cố ý không mang samples ring buffer ra ngoài -- xem chú thích UsageState.
func totalsSnapshot(t *agentTotals) domain.AgentUsageTotals {
	if t == nil {
		return domain.AgentUsageTotals{}
	}
	return domain.AgentUsageTotals{
		Input:        t.Input,
		Output:       t.Output,
		CacheRead:    t.CacheRead,
		CacheWrite:   t.CacheWrite,
		Cost:         t.Cost,
		Saved:        t.Saved,
		CacheCapable: t.CacheCapable,
		CacheBreaks:  t.CacheBreaks,
	}
}

// totalsFromState khôi phục dạng đã persistence thành agentTotals trong memory. samples để trống,
// sau restart sẽ tích lũy lại từ 0, sau vài lượt Record là có thể khôi phục ngữ nghĩa "tỷ lệ hit gần N lần".
func totalsFromState(s domain.AgentUsageTotals) agentTotals {
	return agentTotals{
		Input:        s.Input,
		Output:       s.Output,
		CacheRead:    s.CacheRead,
		CacheWrite:   s.CacheWrite,
		Cost:         s.Cost,
		Saved:        s.Saved,
		CacheCapable: s.CacheCapable,
		CacheBreaks:  s.CacheBreaks,
	}
}

// AgentUsage là snapshot mức dùng tích lũy của một agent (expose cho UI).
type AgentUsage struct {
	Role            string
	Model           string
	Input           int
	Output          int
	CacheRead       int
	CacheWrite      int
	Cost            float64
	Saved           float64
	CacheCapable    bool
	RecentCacheRead int
	RecentInput     int
	RecentSamples   int
}

// PerAgent trả về mức dùng tích lũy của từng role. Kết quả sắp xếp giảm dần theo số CacheRead, role chưa tiêu thụ token thì bỏ qua.
func (t *UsageTracker) PerAgent() []AgentUsage {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]AgentUsage, 0, len(t.perAgent))
	for role, v := range t.perAgent {
		if v.Input == 0 && v.Output == 0 {
			continue
		}
		recentRead, recentInput := recentSums(v)
		out = append(out, AgentUsage{
			Role:            role,
			Input:           v.Input,
			Output:          v.Output,
			CacheRead:       v.CacheRead,
			CacheWrite:      v.CacheWrite,
			Cost:            v.Cost,
			Saved:           v.Saved,
			CacheCapable:    v.CacheCapable,
			RecentCacheRead: recentRead,
			RecentInput:     recentInput,
			RecentSamples:   len(v.samples),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CacheRead != out[j].CacheRead {
			return out[i].CacheRead > out[j].CacheRead
		}
		return out[i].Input > out[j].Input
	})
	return out
}

// PerModel trả về mức dùng tích lũy của từng model. Kết quả sắp xếp giảm dần theo chi phí, sau đó theo lượng input giảm dần.
func (t *UsageTracker) PerModel() []AgentUsage {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]AgentUsage, 0, len(t.perModel))
	for model, v := range t.perModel {
		if v.Input == 0 && v.Output == 0 {
			continue
		}
		out = append(out, AgentUsage{
			Model:        model,
			Input:        v.Input,
			Output:       v.Output,
			CacheRead:    v.CacheRead,
			CacheWrite:   v.CacheWrite,
			Cost:         v.Cost,
			Saved:        v.Saved,
			CacheCapable: v.CacheCapable,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Input > out[j].Input
	})
	return out
}

// resolveCost đồng thời trả về cost / saved / capable của message lần này.
//   - cost: nếu registry hit thì nhân cộng theo 4 mục; không hit thì quay về cost provider tự mang theo
//   - saved: chỉ > 0 khi registry hit, CacheRead > 0, và InputCost > CacheReadCost
//   - capable: registry hit và model này có CacheReadCostPer1M > 0 -> đã biết hỗ trợ prompt caching
//
// modelName ưu tiên dùng giá trị caller truyền vào (_meta.model từ session jsonl khi replay).
func (t *UsageTracker) resolveCost(modelName string, u agentcore.Usage) (cost, saved float64, capable bool) {
	if entry, ok := models.DefaultRegistry().Resolve(modelName); ok {
		c := computeCost(u, *entry)
		s := computeSaved(u, *entry)
		canCache := entry.CacheReadCostPer1M > 0
		if c > 0 {
			return c, s, canCache
		}
	}
	if u.Cost != nil {
		return u.Cost.Total, 0, false
	}
	return 0, 0, false
}

// agentRoleName chuẩn hóa tên subagent thành tên role.
// architect_short/mid/long đều quy về architect; các tên khác trả nguyên dạng.
func agentRoleName(agentName string) string {
	if strings.HasPrefix(agentName, "architect_") {
		return "architect"
	}
	return agentName
}

// computeCost tính chi phí USD của lần gọi này theo đơn giá $/1M tokens.
//
// Tiền đề ngữ nghĩa (được litellm đảm bảo thống nhất ở mọi provider, xem các điểm lắp ráp Usage trong
// anthropic.go / bedrock.go / openai.go / gemini.go / compat.go):
//
//	u.Input  = toàn bộ input token, **bao gồm** CacheRead; không bao gồm CacheWrite
//	u.Output = output token
//
// Vì vậy nonCachedInput = u.Input - u.CacheRead đúng với mọi provider.
// Nhánh fallback được giữ lại để tránh crash nếu trong tương lai provider nào đó trả dữ liệu bẩn.
func computeCost(u agentcore.Usage, e models.ModelEntry) float64 {
	nonCachedInput := u.Input - u.CacheRead
	if nonCachedInput < 0 {
		nonCachedInput = u.Input
	}
	c := 0.0
	c += float64(nonCachedInput) * e.InputCostPer1M / 1_000_000
	c += float64(u.Output) * e.OutputCostPer1M / 1_000_000
	c += float64(u.CacheRead) * e.CacheReadCostPer1M / 1_000_000
	c += float64(u.CacheWrite) * e.CacheWriteCostPer1M / 1_000_000
	return c
}

// computeSaved ước tính số USD tiết kiệm được khi CacheRead hit so với "tính phí theo giá input thông thường".
// Lưu ý không khấu trừ phần premium của CacheWrite -- nó thuộc khoản đầu tư cần thiết để "lót đường cho hit sau",
// lợi ích thật sự được thu hồi nhờ CacheRead tích lũy về sau.
func computeSaved(u agentcore.Usage, e models.ModelEntry) float64 {
	if u.CacheRead <= 0 || e.InputCostPer1M <= 0 {
		return 0
	}
	delta := e.InputCostPer1M - e.CacheReadCostPer1M
	if delta <= 0 {
		return 0
	}
	return float64(u.CacheRead) * delta / 1_000_000
}
