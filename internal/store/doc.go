// Package store cung cấp lưu trữ bền vững dựa trên hệ thống tệp.
//
// Kiến trúc: 1 nền tảng IO + nhiều kho con + 1 gốc tổ hợp.
// Mỗi kho con giữ một phiên bản IO độc lập và một sync.RWMutex độc lập.
// Việc đọc ghi ở các miền chính (Progress, Outline, Drafts, Summaries, v.v.) không chặn lẫn nhau;
// WorldStore gộp nhiều miền nhỏ tần suất thấp để dùng chung một khóa.
//
// Gốc tổ hợp Store giữ tham chiếu tới tất cả kho con, và điều phối tuần tự các thao tác liên miền
// (ExpandArc, AppendVolume, ClearHandledSteer); nhiều tệp không tạo thành một lần commit nguyên tử theo giao dịch,
// lời gọi dựa vào thứ tự ghi an toàn, lỗi tường minh và phát lại lũy đẳng với cùng tham số để khôi phục.
//
// Phân chia kho con:
//   - ProgressStore: trạng thái tiến độ chính (meta/progress.json)
//   - OutlineStore: tiền đề, dàn ý (phẳng/phân cấp), la bàn
//   - DraftStore: ý tưởng chương, bản nháp, bản hoàn chỉnh
//   - SummaryStore: tóm tắt chương/cung/quyển
//   - RunMetaStore: siêu dữ liệu chạy (mô hình, lịch sử can thiệp)
//   - SignalStore: tệp tín hiệu dùng một lần (khôi phục PendingCommit)
//   - CheckpointStore: checkpoint cấp step (meta/checkpoints.jsonl)
//   - RuntimeStore: hàng đợi sự kiện thời gian chạy (meta/runtime/*.jsonl)
//   - CharacterStore: hồ sơ nhân vật, ảnh chụp nhanh trạng thái
//   - WorldStore: dòng thời gian,mồi báo trước, quan hệ, thay đổi trạng thái, quy tắc thế giới, quy tắc phong cách, duyệt xét
package store
