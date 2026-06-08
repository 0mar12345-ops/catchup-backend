package models

import "time"

// AbsenceUploadRow is a single CSV row received from the teacher upload endpoint.
type AbsenceUploadRow struct {
	StudentEmail string
	Date         time.Time
	Reason       string
	Excused      bool
}

// AbsenceUploadSummary is returned to the client after the CSV has been processed.
type AbsenceUploadSummary struct {
	StudentEmail string `json:"student_email"`
	StudentName  string `json:"student_name,omitempty"`
	CourseName   string `json:"course_name,omitempty"`
	Date         string `json:"date"`
	Reason       string `json:"reason,omitempty"`
	Excused      bool   `json:"excused"`
	MissedLabel  string `json:"missed_label"`
}

// AbsenceUploadResult summarises the upload outcome.
type AbsenceUploadResult struct {
	ScheduleType   string                 `json:"schedule_type"`
	TotalRows      int                    `json:"total_rows"`
	CreatedRecords int                    `json:"created_records"`
	Warnings       []string               `json:"warnings,omitempty"`
	Summary        []AbsenceUploadSummary `json:"summary"`
}
