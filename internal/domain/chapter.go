package domain

import (
	"fmt"
	"unicode/utf8"
)

// ReviewInterval Khoảng thời gian duyệt tổng thể (mỗi N chương kích hoạt một lần).
const ReviewInterval = 5

// ShouldReview Dựa trên số chương đã hoàn thành để xác định có cần duyệt tổng thể hay không (chế độ truyện ngắn/truyện vừa).
func ShouldReview(completedCount int) (bool, string) {
	if completedCount > 0 && completedCount%ReviewInterval == 0 {
		return true, fmt.Sprintf("Đã hoàn thành %d chương, kích hoạt duyệt tổng thể", completedCount)
	}
	return false, ""
}

// ShouldArcReview Trong chế độ truyện dài, xác định có cần đánh giá cấp arc/cấp volume hay không.
func ShouldArcReview(isArcEnd, isVolumeEnd bool, volume, arc int) (bool, string) {
	if isVolumeEnd {
		return true, fmt.Sprintf("Kết thúc volume %d arc %d (kết thúc volume), kích hoạt đánh giá cấp arc + cấp volume", volume, arc)
	}
	if isArcEnd {
		return true, fmt.Sprintf("Kết thúc volume %d arc %d, kích hoạt đánh giá cấp arc", volume, arc)
	}
	return false, ""
}

// WordCount Đếm số lượng từ theo rune.
func WordCount(content string) int {
	return utf8.RuneCountInString(content)
}
