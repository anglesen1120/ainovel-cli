package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/models"
)

func TestUsageTrackerReplaySessionsReadsWorkerLogs(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "meta", "sessions", "agents")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	rec := sessionRecord{
		Role: agentcore.RoleAssistant,
		Usage: &agentcore.Usage{
			Input: 1200, Output: 300, CacheRead: 800,
		},
		Meta: &sessionRecordMeta{Provider: "openrouter", Model: "test-model"},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(sessionsDir, "writer-ch01.jsonl"), data, 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	tk := NewUsageTracker(nil, nil)
	n, err := tk.ReplaySessions(dir)
	if err != nil {
		t.Fatalf("ReplaySessions: %v", err)
	}
	if n != 1 {
		t.Fatalf("replayed messages = %d, want 1", n)
	}
	_, input, output, cacheRead, _ := tk.Totals()
	if input != 1200 || output != 300 || cacheRead != 800 {
		t.Fatalf("replayed totals = input:%d output:%d cache:%d", input, output, cacheRead)
	}
}

// makeUsageMsg tao mot thong diep ma callback OnMessage co the chap nhan (co Usage).
// Role phai duoc dat ro thanh assistant: UsageTracker.Record hien loc theo role,
// chi thong diep assistant moi duoc cong don (cac role khac von khong co usage).
func makeUsageMsg(input, cacheRead, cacheWrite, output int) agentcore.AgentMessage {
	return agentcore.Message{
		Role: agentcore.RoleAssistant,
		Usage: &agentcore.Usage{
			Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite,
		},
	}
}

// Test_pushSample_RingBuffer xac minh ngu nghia xoay vong cua cua so truot:
// N lan dau append truc tiep; sau do ghi de muc cu nhat theo sampleIdx. recentSums luon phan anh "N lan gan nhat".
func Test_pushSample_RingBuffer(t *testing.T) {
	var tot agentTotals

	for i := 1; i <= recentSampleCap; i++ {
		pushSample(&tot, i, i*100)
	}
	if got := len(tot.samples); got != recentSampleCap {
		t.Fatalf("after %d pushes, samples len=%d want %d", recentSampleCap, got, recentSampleCap)
	}

	pushSample(&tot, 999, 99900)
	if got := len(tot.samples); got != recentSampleCap {
		t.Fatalf("after overflow, samples len=%d want %d (no growth)", got, recentSampleCap)
	}
	cacheRead, input := recentSums(&tot)
	expectedCacheRead := 999
	expectedInput := 99900
	for i := 2; i <= recentSampleCap; i++ {
		expectedCacheRead += i
		expectedInput += i * 100
	}
	if cacheRead != expectedCacheRead || input != expectedInput {
		t.Fatalf("recentSums after overflow = (%d, %d), want (%d, %d)",
			cacheRead, input, expectedCacheRead, expectedInput)
	}
}

// Test_UsageTracker_RecordAccumulates xac minh Record cong don dung cho nhieu role,
// hop nhat tong the = tong cua tat ca role; per-role doc lap voi nhau.
func Test_UsageTracker_RecordAccumulates(t *testing.T) {
	tk := NewUsageTracker(nil, nil) // modelSet=nil -> di theo Cost cua provider lam du phong, khong anh huong logic cong don

	tk.Record("writer", "", makeUsageMsg(1000, 800, 0, 200))
	tk.Record("writer", "", makeUsageMsg(1500, 1200, 100, 300))
	tk.Record("editor", "", makeUsageMsg(500, 0, 0, 100))

	cost, in, out, cr, cw := tk.Totals()
	if in != 3000 || out != 600 || cr != 2000 || cw != 100 {
		t.Fatalf("totals = (in=%d out=%d cr=%d cw=%d), want (3000 600 2000 100)", in, out, cr, cw)
	}
	if cost != 0 {
		t.Errorf("cost should be 0 when modelSet=nil and no provider Cost, got %v", cost)
	}

	per := tk.PerAgent()
	if len(per) != 2 {
		t.Fatalf("per-agent len=%d want 2", len(per))
	}
	// PerAgent sap xep CacheRead giam dan: writer (2000) phai dung truoc editor (0)
	if per[0].Role != "writer" || per[1].Role != "editor" {
		t.Fatalf("per-agent order = %s,%s want writer,editor", per[0].Role, per[1].Role)
	}
	if per[0].Input != 2500 || per[0].CacheRead != 2000 {
		t.Errorf("writer totals = (in=%d cr=%d), want (2500 2000)", per[0].Input, per[0].CacheRead)
	}
}

// Test_UsageTracker_ArchitectAliasNormalized xac minh architect_short/mid/long
// deu duoc chuan hoa ve cung mot key "architect" (tranh viec cac role con do /model chuyen doi bi tach thanh nhieu dong).
func Test_UsageTracker_ArchitectAliasNormalized(t *testing.T) {
	tk := NewUsageTracker(nil, nil)
	tk.Record("architect_short", "", makeUsageMsg(100, 50, 0, 20))
	tk.Record("architect_mid", "", makeUsageMsg(200, 100, 0, 40))
	tk.Record("architect_long", "", makeUsageMsg(300, 150, 0, 60))

	per := tk.PerAgent()
	if len(per) != 1 {
		t.Fatalf("aliases must merge to single role, got %d entries: %+v", len(per), per)
	}
	if per[0].Role != "architect" {
		t.Fatalf("merged role name = %q, want architect", per[0].Role)
	}
	if per[0].Input != 600 || per[0].CacheRead != 300 {
		t.Errorf("merged totals = (in=%d cr=%d), want (600 300)", per[0].Input, per[0].CacheRead)
	}
}

func Test_UsageTracker_PerModelAccumulates(t *testing.T) {
	tk := NewUsageTracker(nil, nil)
	tk.accumulate("writer", "openrouter", "model-a", agentcore.Usage{Input: 1000, Output: 200, CacheRead: 700})
	tk.accumulate("editor", "openrouter", "model-b", agentcore.Usage{Input: 500, Output: 100})
	tk.accumulate("writer", "openrouter", "model-a", agentcore.Usage{Input: 300, Output: 80, CacheRead: 200})

	perModel := tk.PerModel()
	if len(perModel) != 2 {
		t.Fatalf("per-model len=%d want 2", len(perModel))
	}
	seen := map[string]AgentUsage{}
	for _, m := range perModel {
		seen[m.Model] = m
	}
	if seen["openrouter/model-a"].Input != 1300 || seen["openrouter/model-a"].CacheRead != 900 {
		t.Errorf("model-a totals = %+v", seen["openrouter/model-a"])
	}
	if seen["openrouter/model-b"].Output != 100 {
		t.Errorf("model-b totals = %+v", seen["openrouter/model-b"])
	}

	snap := tk.Snapshot()
	restored := NewUsageTracker(nil, nil)
	restored.applyState(snap)
	if got := restored.PerModel(); len(got) != 2 {
		t.Fatalf("restored per-model len=%d want 2: %+v", len(got), got)
	}
}

func Test_UsageTracker_RecordUsesActualUsageModel(t *testing.T) {
	tk := NewUsageTracker(nil, nil)
	tk.Record("writer", "", agentcore.Message{
		Role: agentcore.RoleAssistant,
		Usage: &agentcore.Usage{
			Provider: "openrouter",
			Model:    "google/gemini-2.5-pro",
			Input:    1000,
			Output:   200,
		},
	})

	perModel := tk.PerModel()
	if len(perModel) != 1 {
		t.Fatalf("per-model len=%d want 1: %+v", len(perModel), perModel)
	}
	if perModel[0].Model != "openrouter/google/gemini-2.5-pro" {
		t.Fatalf("model key = %q, want openrouter/google/gemini-2.5-pro", perModel[0].Model)
	}
	if perModel[0].Input != 1000 || perModel[0].Output != 200 {
		t.Fatalf("model totals = %+v", perModel[0])
	}
}

func Test_UsageTracker_ProviderOnlyDoesNotInventModelKey(t *testing.T) {
	tk := NewUsageTracker(nil, nil)
	tk.Record("writer", "", agentcore.Message{
		Role: agentcore.RoleAssistant,
		Usage: &agentcore.Usage{
			Provider: "openrouter",
			Input:    1000,
			Output:   200,
		},
	})

	if got := tk.PerModel(); len(got) != 0 {
		t.Fatalf("provider-only usage must not create model stats without a model, got %+v", got)
	}
}

// Test_UsageTracker_RecentWindowReflectsLatest xac minh cua so truot phan anh "N lan gan nhat",
// khong bi muc hit thap ban dau keo lui -- day chinh la van de P1 can giai quyet: "giai doan dau keo lui vs trang thai on dinh hit thap".
func Test_UsageTracker_RecentWindowReflectsLatest(t *testing.T) {
	tk := NewUsageTracker(nil, nil)

	// 5 lan dau hit cuc thap (kich ban chuong dau)
	for i := 0; i < 5; i++ {
		tk.Record("writer", "", makeUsageMsg(1000, 0, 0, 200))
	}
	// 8 lan sau (>5) hit cao (kich ban trang thai on dinh)
	for i := 0; i < 8; i++ {
		tk.Record("writer", "", makeUsageMsg(1000, 900, 0, 200))
	}

	per := tk.PerAgent()
	if len(per) != 1 {
		t.Fatalf("len=%d want 1", len(per))
	}
	w := per[0]

	// Cong don: trong 13 lan co 8 lan hit -> 7200/13000 ~= 55.4%
	cumulativeRate := float64(w.CacheRead) / float64(w.Input) * 100
	if cumulativeRate < 50 || cumulativeRate > 60 {
		t.Errorf("cumulative hit rate = %.1f%%, want ~55%%", cumulativeRate)
	}

	// Cua so truot: trong 10 lan gan nhat co 8 lan hit cao + 2 lan zero hit -> 7200/10000 = 72%
	if w.RecentSamples != recentSampleCap {
		t.Errorf("recent samples = %d, want %d (window full)", w.RecentSamples, recentSampleCap)
	}
	recentRate := float64(w.RecentCacheRead) / float64(w.RecentInput) * 100
	if recentRate < 70 || recentRate > 75 {
		t.Errorf("recent hit rate = %.1f%%, want ~72%% (proves window dropped early misses)", recentRate)
	}
	// Diem chinh: N lan gan day cao hon cong don ro ret, chung minh cac gia tri 0 ban dau da bi loai khoi cua so
	if recentRate <= cumulativeRate {
		t.Errorf("recent (%.1f%%) must exceed cumulative (%.1f%%) once window slides past early misses",
			recentRate, cumulativeRate)
	}
}

// Test_computeSaved xac minh thuat toan saved: CacheRead x (gia Input - gia CacheRead);
// tra ve 0 khi chenh lech gia <= 0 hoac InputCost <= 0 (khong khau tru phu phi CacheWrite).
func Test_computeSaved(t *testing.T) {
	cases := []struct {
		name  string
		usage agentcore.Usage
		entry models.ModelEntry
		want  float64
	}{
		{
			name:  "anthropic 5m hit tiet kiem 90%",
			usage: agentcore.Usage{Input: 100_000, CacheRead: 80_000},
			entry: models.ModelEntry{InputCostPer1M: 3.0, CacheReadCostPer1M: 0.3},
			want:  80_000 * (3.0 - 0.3) / 1_000_000, // 0.216
		},
		{
			name:  "khong co hit saved=0",
			usage: agentcore.Usage{Input: 100_000, CacheRead: 0},
			entry: models.ModelEntry{InputCostPer1M: 3.0, CacheReadCostPer1M: 0.3},
			want:  0,
		},
		{
			name:  "model chua duoc dinh gia saved=0",
			usage: agentcore.Usage{Input: 100_000, CacheRead: 50_000},
			entry: models.ModelEntry{InputCostPer1M: 0, CacheReadCostPer1M: 0},
			want:  0,
		},
		{
			name:  "chenh lech gia bat thuong saved=0",
			usage: agentcore.Usage{Input: 100_000, CacheRead: 50_000},
			entry: models.ModelEntry{InputCostPer1M: 1.0, CacheReadCostPer1M: 2.0}, // cache lai dat hon
			want:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeSaved(tc.usage, tc.entry)
			if got != tc.want {
				t.Errorf("computeSaved=%v want %v", got, tc.want)
			}
		})
	}
}

// Test_UsageTracker_CacheCapableSticky xac minh CacheCapable mot khi duoc dat true thi khong lui lai.
// Trong lich su da tung chay model ho tro cache -> du lieu hit cong don hop le; giua chung chuyen sang model khong ho tro thi khong duoc lam co lui lai.
//
// Gan truc tiep perAgent de mo phong bang cach tao du lieu (duong resolveCost can ModelSet+Registry, tang tich hop da bao phu).
func Test_UsageTracker_CacheCapableSticky(t *testing.T) {
	tk := NewUsageTracker(nil, nil)

	// Mo phong "da tung chay model ho tro cache + co hit"
	tk.perAgent["writer"] = &agentTotals{
		Input: 1000, CacheRead: 500, Output: 200, CacheCapable: true,
	}
	// Sau do them mot "lan goi model khong ho tro cache"
	tk.Record("writer", "", makeUsageMsg(500, 0, 0, 100))

	per := tk.PerAgent()
	if len(per) != 1 || per[0].Role != "writer" {
		t.Fatalf("expected single writer entry, got %+v", per)
	}
	if !per[0].CacheCapable {
		t.Errorf("CacheCapable must remain true after switching to non-capable model")
	}
	if per[0].CacheRead != 500 || per[0].Input != 1500 {
		t.Errorf("totals after merge = (in=%d cr=%d), want (1500 500)",
			per[0].Input, per[0].CacheRead)
	}
}

// Test_UsageTracker_PerAgentSkipsZero xac minh role chua tieu thu token khong xuat hien trong PerAgent.
func Test_UsageTracker_PerAgentSkipsZero(t *testing.T) {
	tk := NewUsageTracker(nil, nil)
	// Tao mot role nhung khong tieu thu token (truong hop cuc doan)
	tk.perAgent["ghost"] = &agentTotals{}
	tk.Record("writer", "", makeUsageMsg(100, 50, 0, 20))

	per := tk.PerAgent()
	if len(per) != 1 || per[0].Role != "writer" {
		t.Fatalf("ghost role with zero tokens must be skipped, got %+v", per)
	}
}

// Test_UsageTracker_MissingAssistantUsageCounted xac minh missingAssistantUsage
// bien gioi xac dinh cua bo dem:
//   - Duong cong don chi xem Usage != nil (khong troi chat Role)
//   - Duong chan doan yeu cau Role=Assistant va Content khong rong -- nhu vay moi giong "mot phan hoi LLM that su nhung
//     khong lay duoc usage", tuong ung bieu hien dien hinh khi upstream streaming khong gui chunk final OpenAI include_usage.
//     Cac truong hop khac (thong diep user/tool, assistant co content rong)
//     deu khong tinh la missing.
func Test_UsageTracker_MissingAssistantUsageCounted(t *testing.T) {
	tk := NewUsageTracker(nil, nil)

	withContent := func(text string) agentcore.Message {
		return agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock(text)},
		}
	}

	// assistant + co Content + nil Usage -> trong nhu phan hoi that nhung thieu usage, tinh vao chan doan
	tk.Record("writer", "", withContent("hi"))
	tk.Record("writer", "", withContent("again"))
	// assistant nhung Content rong -> duong khoi phuc bat thuong hoac thong diep giu cho, khong tinh missing
	tk.Record("writer", "", agentcore.Message{Role: agentcore.RoleAssistant})
	// thong diep user/tool von khong mang usage, du Content co rong hay khong deu khong tinh missing
	tk.Record("writer", "", agentcore.Message{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("u")}})
	tk.Record("writer", "", agentcore.Message{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock("t")}})
	// binh thuong co usage -> di duong cong don, khong tinh vao chan doan
	tk.Record("writer", "", makeUsageMsg(100, 50, 0, 20))

	if got := tk.MissingAssistantUsage(); got != 2 {
		t.Errorf("MissingAssistantUsage=%d, want 2", got)
	}
	_, in, _, _, _ := tk.Totals()
	if in != 100 {
		t.Errorf("duong binh thuong cong don bi pha vo, input=%d want 100", in)
	}
}

// Test_UsageTracker_CacheCapableFromFacts xac minh CacheCapable khi registry khong tim thay model do
// van co the dua tren "su that" de danh dau true: cac model tu dung / backend proxy noi dia thuong khong nam trong
// chi muc pricing cua BerriAI/litellm, resolveCost tra ve capable=false; nhung chi can backend that su tra ve
// CacheRead hoac CacheWrite > 0 thi chung minh model do khach quan ho tro prompt cache, dong per-role
// khong nen hien thi "chua bat".
func Test_UsageTracker_CacheCapableFromFacts(t *testing.T) {
	tk := NewUsageTracker(nil, nil) // modelSet=nil -> resolveCost luon capable=false

	// Mot lan co CacheWrite (mo phong lan dau ghi vao cache, registry khong danh dau capable, nhung su that chung minh co ho tro)
	tk.Record("writer", "", makeUsageMsg(1000, 0, 200, 100))
	per := tk.PerAgent()
	if len(per) != 1 || !per[0].CacheCapable {
		t.Fatalf("CacheWrite > 0 phai danh dau ngay CacheCapable=true, got %+v", per)
	}
	if !tk.OverallCacheCapable() {
		t.Errorf("overall CacheCapable cung phai duoc dong bo dat true")
	}

	// Chieu nguoc lai: role hoan toan khong co hoat dong cache, CacheCapable phai giu false
	tk.Record("editor", "", makeUsageMsg(500, 0, 0, 100))
	per = tk.PerAgent()
	for _, a := range per {
		if a.Role == "editor" && a.CacheCapable {
			t.Errorf("editor khong co CacheRead/Write trong suot qua trinh, CacheCapable khong nen bi danh dau sai thanh true")
		}
	}
}

// Test_UsageTracker_AccumulatesAnyRoleWithUsage xac minh duong cong don duoc tach khoi Role:
// ngay ca sau nay mot adapter nao do lap rap usage vao message cua role khong phai assistant,
// van co the cong don chinh xac. Bao ve contract "quy tac lap rap va quy tac cong don duoc tach rieng".
func Test_UsageTracker_AccumulatesAnyRoleWithUsage(t *testing.T) {
	tk := NewUsageTracker(nil, nil)
	// Tao mot thong diep khong phai assistant co Usage, ve ly thuyet khong qua pho bien
	hypothetical := agentcore.Message{
		Role:  agentcore.RoleSystem,
		Usage: &agentcore.Usage{Input: 200, Output: 50, CacheRead: 100},
	}
	tk.Record("writer", "", hypothetical)

	_, in, out, cr, _ := tk.Totals()
	if in != 200 || out != 50 || cr != 100 {
		t.Errorf("khong cong don theo truong Usage, got (in=%d out=%d cr=%d) want (200 50 100)", in, out, cr)
	}
	if tk.MissingAssistantUsage() != 0 {
		t.Errorf("co Usage thi khong nen tinh vao missing")
	}
}

// Test_UsageTracker_OnCostCallback xac minh diem dau noi cua linh gac ngan sach: sau moi lan ghi so,
// callback ngoai lock mang theo chi phi cong don moi nhat (bao gom duong provider tu bao cost).
func Test_UsageTracker_OnCostCallback(t *testing.T) {
	tk := NewUsageTracker(nil, nil)
	var got []float64
	tk.SetOnCost(func(total float64) { got = append(got, total) })

	msg := func(cost float64) agentcore.AgentMessage {
		return agentcore.Message{
			Role:  agentcore.RoleAssistant,
			Usage: &agentcore.Usage{Input: 100, Output: 10, Cost: &agentcore.Cost{Total: cost}},
		}
	}
	tk.Record("writer", "", msg(0.5))
	tk.Record("writer", "", msg(0.25))

	if len(got) != 2 || got[0] != 0.5 || got[1] != 0.75 {
		t.Fatalf("onCost should carry growing totals, got %v", got)
	}
}

// Test_UsageTracker_OnMissingUsageOnce xac minh callback vung mu chi kich hoat lan dau.
func Test_UsageTracker_OnMissingUsageOnce(t *testing.T) {
	tk := NewUsageTracker(nil, nil)
	fired := 0
	tk.SetOnMissingUsage(func() { fired++ })

	noUsage := agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("noi dung chinh")}}
	tk.Record("writer", "", noUsage)
	tk.Record("writer", "", noUsage)
	tk.Record("editor", "", noUsage)

	if fired != 1 {
		t.Fatalf("onMissingUsage should fire exactly once, got %d", fired)
	}
}

// TestCacheBreakDetection xac minh bon huong di cua viec phat hien dut chuoi cache:
// tien to tang trong cung phien + hit giam dot ngot -> dut; doi task (spawn moi) -> doi baseline khong so sanh;
// tien to ngan lai (nen trong phien) -> chi reset baseline, khong canh bao; muc giam khong dat nguong kep (tuong doi 5%
// va tuyet doi 2000) -> khong canh bao.
func TestCacheBreakDetection(t *testing.T) {
	tk := NewUsageTracker(nil, nil)

	// Lap baseline: tien to 30k, hit 28k.
	tk.Record("writer", "viet chuong 1", makeUsageMsg(30000, 28000, 0, 100))
	if got := tk.OverallCacheBreaks(); got != 0 {
		t.Fatalf("thong diep dau tien khong nen phan dinh dut, got %d", got)
	}

	// Trong cung phien tien to tang nhung hit giam dot ngot (28k->4k) -> dut.
	tk.Record("writer", "viet chuong 1", makeUsageMsg(34000, 4096, 0, 100))
	if got := tk.OverallCacheBreaks(); got != 1 {
		t.Fatalf("tien to tang + hit giam dot ngot phai phan dinh 1 lan dut, got %d", got)
	}

	// Trong cung phien tien to ngan lai (nen ngu canh, 4.4k < 34k) -> reset baseline, khong canh bao.
	tk.Record("writer", "viet chuong 1", makeUsageMsg(4400, 0, 0, 100))
	if got := tk.OverallCacheBreaks(); got != 1 {
		t.Fatalf("tien to ngan lai phai duoc xem la reset do nen, got %d", got)
	}

	// Tren baseline moi giam nhe (muc giam < nguong tuyet doi 2000) -> khong canh bao.
	tk.Record("writer", "viet chuong 1", makeUsageMsg(36000, 30000, 0, 100))
	tk.Record("writer", "viet chuong 1", makeUsageMsg(38000, 28500, 0, 100))
	if got := tk.OverallCacheBreaks(); got != 1 {
		t.Fatalf("muc giam 1.5k chua vuot nguong tuyet doi thi khong nen canh bao, got %d", got)
	}

	// Doi task = spawn moi = huyet thong cache moi: ngay ca khi tien to cua request dau khong ngan hon request cuoi cua phien truoc
	// (38k -> 40k) va hit giam dot ngot (28.5k->0), cung khong so sanh va khong canh bao. Day la ca hoi quy "bao nham do cac phien ngan lien tiep":
	// chieu phat hien phai can theo do hat phien cua prompt_cache_key.
	tk.Record("writer", "viet chuong 2", makeUsageMsg(40000, 0, 0, 100))
	if got := tk.OverallCacheBreaks(); got != 1 {
		t.Fatalf("doi task thi doi baseline, khong nen so sanh xuyen phien, got %d", got)
	}

	// Lai dut trong phien moi -> canh bao binh thuong (chung minh baseline moi da co hieu luc).
	tk.Record("writer", "viet chuong 2", makeUsageMsg(45000, 38000, 0, 100))
	tk.Record("writer", "viet chuong 2", makeUsageMsg(48000, 5000, 0, 100))
	if got := tk.OverallCacheBreaks(); got != 2 {
		t.Fatalf("dut trong phien moi phai duoc phat hien binh thuong, got %d", got)
	}

	// Muc giam tuong doi <5% (100k->96k, giam 4%) -> khong canh bao (ngay ca khi muc giam tuyet doi 4k > 2000).
	tk.Record("editor", "duyet cung dau", makeUsageMsg(120000, 100000, 0, 100))
	tk.Record("editor", "duyet cung dau", makeUsageMsg(125000, 96000, 0, 100))
	if got := tk.OverallCacheBreaks(); got != 2 {
		t.Fatalf("muc giam tuong doi 4%% chua vuot nguong 5%% thi khong nen canh bao, got %d", got)
	}

	// per-role quy thuoc: dut duoc tinh duoi writer va dua vao Snapshot.
	snap := tk.Snapshot()
	if snap.Overall.CacheBreaks != 2 || snap.PerAgent["writer"].CacheBreaks != 2 {
		t.Fatalf("so dem dut phai vao snapshot: overall=%d writer=%d", snap.Overall.CacheBreaks, snap.PerAgent["writer"].CacheBreaks)
	}
}
