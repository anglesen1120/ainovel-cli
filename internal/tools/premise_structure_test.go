package tools

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestParsePremiseSections(t *testing.T) {
	premise := `# Premise

## Thể loại và tông điệu
huyền huyễn phương Đông, hành trình trưởng thành gai góc.

## Định vị thể loại
dòng nâng cấp huyền huyễn phương Đông, dành cho độc giả thích cao trào đã đời và quan hệ tiến triển.

## Xung đột cốt lõi
nhân vật chính phải lựa chọn giữa quy tắc tông môn và lương tri cá nhân.

## Bước ngoặt giữa chặng
lộ tuyến tu luyện cũ mất tác dụng, buộc phải chuyển sang hệ thống cấm thuật.
`

	sections := parsePremiseSections(premise)
	if sections["Thể loại và tông điệu"] == "" {
		t.Fatalf("expected Thể loại và tông điệu section, got %+v", sections)
	}
	if sections["Định vị thể loại"] == "" {
		t.Fatalf("expected Định vị thể loại section, got %+v", sections)
	}
	if sections["Xung đột cốt lõi"] == "" {
		t.Fatalf("expected Xung đột cốt lõi section, got %+v", sections)
	}
	if sections["Bước ngoặt giữa chặng"] == "" {
		t.Fatalf("expected Bước ngoặt giữa chặng alias normalized to Bước ngoặt giữa chặng, got %+v", sections)
	}
}

func TestPremiseStructure(t *testing.T) {
	premise := `## Thể loại và tông điệu
dòng nâng cấp, thiên về chất gai góc lạnh cứng.

## Định vị thể loại
dòng nâng cấp

## Xung đột cốt lõi
xung đột

## Mục tiêu nhân vật chính
mục tiêu

## Hướng kết cục
kết cục

## Vùng cấm viết
vùng cấm

## Điểm bán khác biệt
điểm bán

## Móc câu khác biệt
móc câu

## Cam kết thực hiện cốt lõi
thực hiện cam kết

## Động cơ câu chuyện
động cơ

## Bước ngoặt giữa chặng
bước ngoặt
`

	structure := premiseStructure(premise, domain.PlanningTierMid)
	if ready, _ := structure["template_ready"].(bool); !ready {
		t.Fatalf("expected template_ready, got %+v", structure)
	}
	missing, _ := structure["missing"].([]string)
	if len(missing) != 0 {
		t.Fatalf("expected no missing headings, got %+v", missing)
	}
}

func TestPremiseStructureShortAcceptsLegacyHeadingAlias(t *testing.T) {
	premise := `## Thể loại và tông điệu
cuộc giải cứu áp lực cao trong một tập.

## Định vị thể loại
phiêu lưu ngắn tập mật độ cao.

## Xung đột cốt lõi
nhân vật chính phải cứu con tin trong một đêm.

## Mục tiêu nhân vật chính
cứu con tin và sống sót rời đi.

## Hướng kết cục
hoàn thành nhiệm vụ nhưng phải trả giá.

## Vùng cấm viết
không mở rộng thành truyện dài kỳ.

## Điểm bán khác biệt
áp lực thời hạn và các cú đảo chiều liên tiếp.

## Móc câu khác biệt
mỗi lựa chọn đều rút ngắn thời gian cứu viện.

## Cam kết thực hiện cốt lõi
cảm giác cấp bách, lựa chọn và đảo chiều.

## Vì sao tác phẩm phù hợp với truyện ngắn/một tập
mâu thuẫn cốt lõi và cung nhân vật đều có thể hoàn tất trong một nhiệm vụ.
`

	structure := premiseStructure(premise, domain.PlanningTierShort)
	if ready, _ := structure["template_ready"].(bool); !ready {
		t.Fatalf("expected short template_ready, got %+v", structure)
	}
}
