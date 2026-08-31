package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/internal/diag"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Loại thông báo
type (
	eventMsg       host.Event
	snapshotMsg    host.UISnapshot
	doneMsg        struct{ complete bool } // complete=true hoàn thành toàn bộ sách, false dừng do lỗi
	abortResultMsg struct{ stopped bool }
	bootstrapMsg   struct {
		existing  bool // Đã có tác phẩm; dù khôi phục có thành công hay không cũng phải vào workspace
		resumed   bool
		completed bool // Trong thư mục là một cuốn sách đã hoàn tất: vào workspace ở trạng thái hoàn tất thay vì trang chào mừng
		err       error
	}
	reportLoadedMsg struct {
		reqID      int
		report     diag.Report
		exportPath string // Đường dẫn tuyệt đối của file chẩn đoán đã khử nhạy cảm; rỗng = xuất thất bại
		exportErr  error
		finishedAt time.Time
	}
	startResultMsg   struct{ err error }
	cocreateDeltaMsg struct {
		reqID int
		kind  string // host.CoCreateProgressThinking | host.CoCreateProgressReply
		text  string
	}
	// cocreateStreamItem là payload nội bộ của deltaCh, gửi kind dạng stream cùng văn bản tích lũy tới TUI.
	cocreateStreamItem struct {
		kind string
		text string
	}
	cocreateDoneMsg struct {
		reqID int
		reply host.CoCreateReply
		err   error
	}
	steerResultMsg     struct{ err error }
	continueResultMsg  struct{ err error }
	spinnerTickMsg     time.Time
	toolSpinnerTickMsg time.Time // tick độc lập của spinner công cụ trong luồng sự kiện (nhanh hơn, độc lập với thanh trên cùng/ngôi sao)
	streamDeltaMsg     string    // Phần tăng thêm của token dạng stream
	streamClearMsg     struct{}  // Xóa bộ đệm dạng stream
	streamFlushTickMsg struct{}  // Tiết lưu làm mới stream (chỉ lên lịch khi có dữ liệu chờ flush)
	quitResetMsg       struct{}  // Đặt lại thời gian chờ của Ctrl+C hai lần
)

// --- Hàm Cmd ---

func listenEvents(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-rt.Events()
		if !ok {
			return nil
		}
		return eventMsg(ev)
	}
}

func listenDone(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		_, ok := <-rt.Done()
		if !ok {
			return nil
		}
		snap := rt.Snapshot()
		return doneMsg{complete: snap.Phase == "complete"}
	}
}

func tickSnapshot(rt *host.Host) tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return snapshotMsg(rt.Snapshot())
	})
}

func fetchSnapshot(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		return snapshotMsg(rt.Snapshot())
	}
}

func bootstrapRuntime(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		snapshot := rt.Snapshot()
		msg := bootstrapMsg{
			existing:  snapshot.Phase != "" || snapshot.BookTitle != "",
			completed: snapshot.Phase == "complete",
		}
		label, err := rt.Resume()
		if err != nil {
			msg.err = err
			return msg
		}
		if label == "" {
			if msg.existing {
				return msg
			}
			return nil
		}
		msg.resumed = true
		return msg
	}
}

// resumeBook chạy bù một lần gate khôi phục trong phiên (Resume của bootstrap chỉ chạy một lần khi khởi động):
// Sau khi nhập xong đóng panel, hoặc mở lại bằng /reopen, đều dựa vào nó để quay về workspace sáng tác. Không phát lại hàng đợi sự kiện —— sự kiện của phiên này
// đã được listenEvents thường trú hiển thị; phát lại sẽ echo trùng. Các can thiệp đang chờ xử lý (như hướng viết tiếp được đăng ký bởi /reopen)
// sẽ được Resume đưa qua Arbiter để phân xử và xử lý trước, rồi tiếp tục chạy engine.
func resumeBook(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		snapshot := rt.Snapshot()
		label, err := rt.Resume()
		return bootstrapMsg{
			existing: snapshot.Phase != "" || snapshot.BookTitle != "", completed: snapshot.Phase == "complete",
			resumed: label != "", err: err,
		}
	}
}

func startRuntime(rt *host.Host, prompt string) tea.Cmd {
	return func() tea.Msg {
		// Phía khởi động tạo xác định snapshot quy tắc người dùng của sách này (chuẩn hóa bằng prompt gốc), phải thực hiện trước StartPrepared.
		if err := rt.PrepareUserRules(prompt); err != nil {
			return startResultMsg{err: err}
		}
		err := rt.StartPrepared(prompt)
		return startResultMsg{err: err}
	}
}

func runCoCreate(rt *host.Host, state *cocreateState) tea.Cmd {
	history := state.session.History()
	ctx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	state.deltaCh = make(chan cocreateStreamItem, 64)
	state.doneCh = make(chan cocreateDoneMsg, 1)
	// Giai đoạn đồng sáng tác mang theo tóm tắt trạng thái câu chuyện và tạo ra "brief hướng tiếp theo"; khởi động lạnh thì làm rõ yêu cầu từ đầu. Chữ ký của cả hai giống nhau.
	stream := rt.CoCreateStream
	if state.stage {
		stream = rt.StageCoCreateStream
	}
	start := func() tea.Msg {
		go func() {
			reply, err := stream(ctx, history, func(kind, text string) {
				select {
				case state.deltaCh <- cocreateStreamItem{kind: kind, text: text}:
				default:
				}
			})
			state.doneCh <- cocreateDoneMsg{reply: reply, err: err}
			close(state.deltaCh)
			close(state.doneCh)
		}()
		return nil
	}
	return tea.Batch(start, listenCoCreateDelta(state), listenCoCreateDone(state))
}

func listenCoCreateDelta(state *cocreateState) tea.Cmd {
	if state == nil || state.deltaCh == nil {
		return nil
	}
	// Lấy tham chiếu cục bộ của channel: tránh việc sau này state.deltaCh bị reassign
	// làm closure listen cũ đọc nhầm channel mới (dù luồng hiện tại không kích hoạt, để lại như một bẫy bảo trì không mong muốn).
	reqID := state.reqID
	ch := state.deltaCh
	return func() tea.Msg {
		item, ok := <-ch
		if !ok {
			return nil
		}
		return cocreateDeltaMsg{reqID: reqID, kind: item.kind, text: item.text}
	}
}

func listenCoCreateDone(state *cocreateState) tea.Cmd {
	if state == nil || state.doneCh == nil {
		return nil
	}
	reqID := state.reqID
	ch := state.doneCh
	return func() tea.Msg {
		result, ok := <-ch
		if !ok {
			return nil
		}
		result.reqID = reqID
		return result
	}
}

func steerRuntime(rt *host.Host, text string) tea.Cmd {
	return func() tea.Msg {
		return steerResultMsg{err: rt.Steer(text)}
	}
}

func continueRuntime(rt *host.Host, text string) tea.Cmd {
	return func() tea.Msg {
		err := rt.Continue(text)
		return continueResultMsg{err: err}
	}
}

// resumeFromCoCreate tiêm brief hướng tiếp theo do giai đoạn đồng sáng tác tạo ra và khôi phục sáng tác.
// Tái sử dụng continueResultMsg: thành công thì nối tiếp bằng listenDone để chạy tiếp, thất bại thì echo lỗi.
func resumeFromCoCreate(rt *host.Host, draft string) tea.Cmd {
	return func() tea.Msg {
		err := rt.ResumeFromCoCreate(draft)
		return continueResultMsg{err: err}
	}
}

// cancelCoCreate từ bỏ giai đoạn đồng sáng tác: xóa cờ chiếm dụng, giữ trạng thái tạm dừng. Sự kiện chảy ngược qua channel events, không cần trả về thông báo.
func cancelCoCreate(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		rt.CancelCoCreate()
		return nil
	}
}

func abortRuntime(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		return abortResultMsg{stopped: rt.Abort()}
	}
}

func loadReport(dir string, reqID int) tea.Cmd {
	return func() tea.Msg {
		s := store.NewStore(dir)
		// Diagnose = chẩn đoán sáng tác + kiểm tra runtime, Finding runtime cũng được đưa vào báo cáo trên màn hình.
		rep, rc := diag.Diagnose(s)
		// Tái sử dụng rep+rc để ghi file chẩn đoán đã khử nhạy cảm (xuất thất bại không ảnh hưởng báo cáo trên màn hình).
		exportPath, exportErr := diag.WriteExport(s, rep, rc)
		return reportLoadedMsg{
			reqID:      reqID,
			report:     rep,
			exportPath: exportPath,
			exportErr:  exportErr,
			finishedAt: time.Now(),
		}
	}
}

func tickSpinner() tea.Cmd {
	return tea.Tick(350*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

// tickToolSpinner điều khiển spinner của dòng "đang tiến hành" trong luồng sự kiện. Độc lập với tickSpinner, nhịp nhanh hơn (150ms).
func tickToolSpinner() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return toolSpinnerTickMsg(t)
	})
}

// tickStreamFlush gộp các phần tăng thêm dạng stream trong một cửa sổ 16ms. Nó được khởi động bởi delta đầu tiên chờ flush,
// sau khi flush xong thì dừng, khi rảnh sẽ không liên tục đánh thức TUI.
func tickStreamFlush() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg {
		return streamFlushTickMsg{}
	})
}

func listenStream(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		delta, ok := <-rt.Stream()
		if !ok {
			return nil
		}
		// sentinel được dispatch thành streamClearMsg, bảo đảm cùng đến TUI theo thứ tự emit với delta bình thường trong cùng một channel
		//. Khi dùng hai channel, clearCh và streamCh không có thứ tự với nhau, ✻ header thường bị
		// nhét nhầm vào cuối đoạn thinking trước đó.
		if delta == host.StreamClearSentinel {
			return streamClearMsg{}
		}
		return streamDeltaMsg(delta)
	}
}
