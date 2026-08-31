// Package imp implements the phased semantic import pipeline for external novels (docs/import-pipeline.md).
//
// The model is responsible for understanding open semantics; the code is responsible for coordinates,
// coverage, types, hashes, order, and idempotence; all semantic artifacts are validated in a separate
// workspace (meta/import/) before being published to the official book state. The next action is derived
// only from artifacts (NextAction), with no drifting stage enum stored, and recovery does not depend on from=N.
package imp

import "time"

// Options controls one import. During recovery, fields may be empty and are derived directly from the active
// workspace and the saved Intent.
type Options struct {
	SourcePath      string // required for a new import; may be empty during recovery
	AutoConfirm     bool   // --yes: automatically accept segmentation after coverage validation passes
	StoryResolution string // --story=open|closed: preselect only when synthesis returns uncertain
	ContinueAfter   bool   // --continue: do not create an import-complete Hold
	Guidance        string // --guide: natural-language segmentation guidance; after being written to the workspace, it naturally re-detects old segmentation mismatches
	// AcceptSegmentation: explicit human confirmation after TUI preview (y). Allow the current segmentation once, without writing intent;
	// difference from --yes: --yes is a blind authorization without seeing the preview, does not allow segmentations with tolerance notes (Notes); y is a ruling after seeing the preview.
	AcceptSegmentation bool
}

// intent extracts the user authorizations that need to be persisted from Options.
func (o Options) intent() Intent {
	return Intent{
		Version:             workspaceSchemaVersion,
		AutoConfirm:         o.AutoConfirm,
		StoryResolution:     o.StoryResolution,
		ContinueAfterImport: o.ContinueAfter,
	}
}

// Stage represents the current stage of the import flow, used only for UI display, not as the source of truth for recovery (RFC §14.1).
type Stage string

const (
	StageIngesting            Stage = "ingesting"
	StageSegmenting           Stage = "segmenting"
	StageAwaitingConfirmation Stage = "awaiting_confirmation"
	StageAnalyzing            Stage = "analyzing"
	StageSynthesizing         Stage = "synthesizing"
	StageAwaitingStoryStatus  Stage = "awaiting_story_status"
	StageValidating           Stage = "validating"
	StagePublishing           Stage = "publishing"
	StageDone                 Stage = "done"
	StageError                Stage = "error"
)

// Event is a progress event emitted externally by the import flow. Event is a projection and does not participate in recovery.
type Event struct {
	Time      time.Time
	Stage     Stage
	Current   int       // chapter/interval progress
	Total     int       // total count
	Message   string    // human-readable description
	Level     string    // ""=normal progress; "warn"=warning states such as backoff retries/validation re-asks
	Key       string    // when non-empty, consecutive events with the same Key update in place in the UI (e.g. 7 backoffs changing in one line), aligning with the event panel ID mechanism
	RetryAt   time.Time // non-zero = deadline of the next retry; UI renders a per-second countdown accordingly, clearing at the moment (request already in flight)
	Err       error     // carried when StageError
	Continued bool      // when StageDone, set by Host: whether Engine has been automatically handed off and started (--continue × auto)
}
