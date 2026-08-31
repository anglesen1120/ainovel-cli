#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Trình kiểm tra số lượng từ trong chương
Kiểm tra số lượng từ của tệp chương được chỉ định và cảnh báo khi dưới 3000 từ
"""

import re
import sys
from pathlib import Path

# Sửa lỗi mã hóa trong bảng điều khiển Windows
if sys.platform == 'win32':
    import io
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')


def count_chinese_words(text: str) -> int:
    """Đếm ký tự tiếng Trung (không tính dấu câu và dấu Markdown)."""
    text = re.sub(r'#{1,6}\s*', '', text)
    text = re.sub(r'\*\*(.*?)\*\*', r'\1', text)
    text = re.sub(r'\*(.*?)\*', r'\1', text)
    text = re.sub(r'~~(.*?)~~', r'\1', text)
    text = re.sub(r'`(.*?)`', r'\1', text)
    text = re.sub(r'\[(.*?)\]\(.*?\)', r'\1', text)

    chinese_chars = re.findall(r'[\u4e00-\u9fff]', text)
    return len(chinese_chars)


def extract_content_from_chapter(file_path: Path) -> str:
    """Trích xuất nội dung chính của tệp chương (bỏ tiêu đề và siêu dữ liệu)."""
    content = file_path.read_text(encoding='utf-8')
    lines = content.split('\n')

    content_start = 0
    for i, line in enumerate(lines):
        if line.startswith('#') and '\u7ae0' in line:
            content_start = i + 1
            break

    return '\n'.join(lines[content_start:])


def check_chapter(file_path: str, min_words: int = 3000) -> dict:
    """Kiểm tra số lượng từ của một chương."""
    path = Path(file_path)
    if not path.exists():
        return {'file': str(path), 'exists': False, 'word_count': 0, 'status': 'error', 'message': f'Không tìm thấy tệp: {file_path}'}

    main_content = extract_content_from_chapter(path)
    word_count = count_chinese_words(main_content)
    status = 'pass' if word_count >= min_words else 'fail'
    message = f'Số lượng từ: {word_count}'
    message += ' (✓ Đạt yêu cầu)' if word_count >= min_words else f' (✗ Quá ngắn; yêu cầu ít nhất {min_words} từ)'
    return {'file': str(path), 'exists': True, 'word_count': word_count, 'status': status, 'message': message}


def check_all_chapters(directory: str, pattern: str = '\\u7b2c*.md', min_words: int = 3000) -> list:
    """Kiểm tra mọi tệp chương khớp mẫu trong một thư mục."""
    dir_path = Path(directory)
    if not dir_path.exists():
        print(f'Lỗi: Không tìm thấy thư mục - {directory}')
        return []
    chapter_files = sorted(dir_path.glob(pattern))
    return [check_chapter(str(chapter_file), min_words) for chapter_file in chapter_files]


def print_results(results: list, min_words: int = 3000) -> None:
    """In kết quả kiểm tra."""
    if not results:
        print('Không tìm thấy tệp chương')
        return
    total_words = 0
    passed = 0
    failed = 0
    print('\n' + '=' * 60)
    print('Báo cáo số lượng từ trong chương')
    print('=' * 60)
    for result in results:
        if not result['exists']:
            print(f'\n❌ {result["file"]}')
            print(f'   {result["message"]}')
            continue
        total_words += result['word_count']
        if result['status'] == 'pass':
            passed += 1
            icon = '✅'
        else:
            failed += 1
            icon = '⚠️ '
        print(f'\n{icon} {Path(result["file"]).name}')
        print(f'   {result["message"]}')
    print('\n' + '-' * 60)
    print(f'Tổng: {len(results)} chương | {passed} đạt | {failed} quá ngắn | Tổng số từ: {total_words:,}')
    print('-' * 60)
    if failed > 0:
        print(f'\n⚠️  {failed} chương dưới {min_words} từ; hãy cân nhắc các cách mở rộng sau:')
        print('   - Thêm mô tả chi tiết (bối cảnh, tâm lý, hành động)')
        print('   - Thêm các cảnh đối thoại')
        print('   - Phát triển suy nghĩ nội tâm của nhân vật')
        print('   - Bổ sung câu chuyện nền')
        print('\n   Xem: references/content-expansion.md')


def main() -> None:
    """Điểm vào chính."""
    if len(sys.argv) < 2:
        print('Cách dùng:')
        print('  Kiểm tra một chương: python check_chapter_wordcount.py <đường-dẫn-tệp-chương> [số-từ-tối-thiểu]')
        print('  Kiểm tra mọi chương: python check_chapter_wordcount.py --all <đường-dẫn-thư-mục> [số-từ-tối-thiểu]')
        print('')
        print('Ví dụ:')
        print('  python check_chapter_wordcount.py novels/truyen/chuong01.md')
        print('  python check_chapter_wordcount.py novels/truyen/chuong01.md 3500')
        print('  python check_chapter_wordcount.py --all novels/truyen')
        print('  python check_chapter_wordcount.py --all novels/truyen 3500')
        return
    if sys.argv[1] == '--all':
        if len(sys.argv) < 3:
            print('Lỗi: --all yêu cầu đường dẫn thư mục')
            return
        directory = sys.argv[2]
        min_words = int(sys.argv[3]) if len(sys.argv) > 3 else 3000
        print_results(check_all_chapters(directory, min_words=min_words), min_words)
        return
    file_path = sys.argv[1]
    min_words = int(sys.argv[2]) if len(sys.argv) > 2 else 3000
    print_results([check_chapter(file_path, min_words)], min_words)


if __name__ == '__main__':
    main()
