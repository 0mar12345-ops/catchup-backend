package services

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// AbsenceUploadService parses CSV absence uploads and writes AbsenceRecord documents.
type AbsenceUploadService struct {
	schoolsCollection        *mongo.Collection
	studentsCollection       *mongo.Collection
	enrollmentsCollection    *mongo.Collection
	coursesCollection        *mongo.Collection
	absenceRecordsCollection *mongo.Collection
}

func NewAbsenceUploadService(client *mongo.Client, dbName string) *AbsenceUploadService {
	db := client.Database(dbName)
	return &AbsenceUploadService{
		schoolsCollection:        db.Collection("schools"),
		studentsCollection:       db.Collection("students"),
		enrollmentsCollection:    db.Collection("enrollments"),
		coursesCollection:        db.Collection("courses"),
		absenceRecordsCollection: db.Collection("absence_records"),
	}
}

func (s *AbsenceUploadService) UploadAbsences(ctx context.Context, teacherID, schoolID bson.ObjectID, fileName string, fileBytes []byte) (*models.AbsenceUploadResult, error) {
	if len(fileBytes) == 0 {
		return nil, errors.New("uploaded file is empty")
	}
	if !strings.HasSuffix(strings.ToLower(fileName), ".csv") {
		return nil, errors.New("only csv uploads are supported")
	}

	rows, err := parseAbsenceCSV(fileBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse csv: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("no absence rows were found in the uploaded file")
	}

	scheduleType, err := s.getScheduleType(ctx, schoolID)
	if err != nil {
		return nil, err
	}

	result := &models.AbsenceUploadResult{
		ScheduleType: scheduleType,
		TotalRows:    len(rows),
		Summary:      make([]models.AbsenceUploadSummary, 0, len(rows)),
		Warnings:     make([]string, 0),
	}

	now := time.Now().UTC()

	for _, row := range rows {
		student, err := s.findStudentByEmail(ctx, schoolID, strings.TrimSpace(row.StudentEmail))
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped %s: %v", row.StudentEmail, err))
			continue
		}

		enrollments, err := s.findActiveEnrollments(ctx, schoolID, student.ID)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped %s: %v", student.Email, err))
			continue
		}
		if len(enrollments) == 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("no active enrollment found for %s", student.Email))
			continue
		}

		for _, enrollment := range enrollments {
			course, err := s.findCourseByID(ctx, enrollment.CourseID)
			if err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("course lookup failed for %s: %v", student.Email, err))
				continue
			}

			absenceRecord := models.AbsenceRecord{
				ID:                bson.NewObjectID(),
				SchoolID:          schoolID,
				CourseID:          enrollment.CourseID,
				StudentID:         student.ID,
				AbsentOn:          row.Date.UTC(),
				MarkedByTeacherID: teacherID,
				Source:            "csv_upload",
				IsLocked:          false,
				CreatedAt:         now,
				UpdatedAt:         now,
			}

			if _, err := s.absenceRecordsCollection.InsertOne(ctx, absenceRecord); err != nil {
				return nil, fmt.Errorf("failed to create absence record for %s: %w", student.Email, err)
			}

			result.CreatedRecords++
			result.Summary = append(result.Summary, models.AbsenceUploadSummary{
				StudentEmail: student.Email,
				StudentName:  student.Name,
				CourseName:   course.Name,
				Date:         row.Date.Format("2006-01-02"),
				Reason:       row.Reason,
				Excused:      row.Excused,
				MissedLabel:  missedLabel(scheduleType, row.Date),
			})
		}
	}

	return result, nil
}

func (s *AbsenceUploadService) getScheduleType(ctx context.Context, schoolID bson.ObjectID) (string, error) {
	var doc bson.M
	if err := s.schoolsCollection.FindOne(ctx, bson.M{"_id": schoolID}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "standard", nil
		}
		return "", err
	}

	if raw, ok := doc["schedule_type"]; ok && strings.TrimSpace(fmt.Sprint(raw)) != "" {
		return strings.ToLower(strings.TrimSpace(fmt.Sprint(raw))), nil
	}
	return "standard", nil
}

func (s *AbsenceUploadService) findStudentByEmail(ctx context.Context, schoolID bson.ObjectID, email string) (*models.Student, error) {
	if strings.TrimSpace(email) == "" {
		return nil, errors.New("student email is required")
	}

	var student models.Student
	if err := s.studentsCollection.FindOne(ctx, bson.M{"school_id": schoolID, "email": strings.ToLower(strings.TrimSpace(email))}).Decode(&student); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("student with email %q was not found", email)
		}
		return nil, err
	}
	return &student, nil
}

func (s *AbsenceUploadService) findActiveEnrollments(ctx context.Context, schoolID bson.ObjectID, studentID bson.ObjectID) ([]models.Enrollment, error) {
	cursor, err := s.enrollmentsCollection.Find(ctx, bson.M{"school_id": schoolID, "student_id": studentID, "status": models.EnrollmentActive})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var enrollments []models.Enrollment
	if err := cursor.All(ctx, &enrollments); err != nil {
		return nil, err
	}
	return enrollments, nil
}

func (s *AbsenceUploadService) findCourseByID(ctx context.Context, courseID bson.ObjectID) (*models.Course, error) {
	var course models.Course
	if err := s.coursesCollection.FindOne(ctx, bson.M{"_id": courseID}).Decode(&course); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("course %s was not found", courseID.Hex())
		}
		return nil, err
	}
	return &course, nil
}

func parseAbsenceCSV(data []byte) ([]models.AbsenceUploadRow, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, errors.New("csv must contain a header row and at least one data row")
	}

	headers := normalizeAbsenceHeaders(records[0])
	rows := make([]models.AbsenceUploadRow, 0, len(records)-1)

	for _, record := range records[1:] {
		if absenceAllBlank(record) {
			continue
		}

		studentEmail := absenceCellAt(record, headers, "student_email", "email")
		dateText := absenceCellAt(record, headers, "date", "absence_date")
		reason := absenceCellAt(record, headers, "reason", "note")
		excusedText := absenceCellAt(record, headers, "excused", "is_excused")

		if strings.TrimSpace(studentEmail) == "" {
			continue
		}

		parsedDate, err := time.Parse("2006-01-02", strings.TrimSpace(dateText))
		if err != nil {
			return nil, fmt.Errorf("invalid date %q: %w", dateText, err)
		}

		excused := false
		if strings.EqualFold(strings.TrimSpace(excusedText), "true") || strings.EqualFold(strings.TrimSpace(excusedText), "yes") || strings.TrimSpace(excusedText) == "1" {
			excused = true
		}

		rows = append(rows, models.AbsenceUploadRow{
			StudentEmail: strings.ToLower(strings.TrimSpace(studentEmail)),
			Date:         parsedDate,
			Reason:       strings.TrimSpace(reason),
			Excused:      excused,
		})
	}

	return rows, nil
}

func normalizeAbsenceHeaders(row []string) map[string]int {
	headers := make(map[string]int)
	for i, cell := range row {
		key := strings.ToLower(strings.TrimSpace(cell))
		key = strings.ReplaceAll(key, " ", "_")
		key = strings.ReplaceAll(key, "-", "_")
		key = strings.ReplaceAll(key, "/", "_")
		headers[key] = i
	}
	return headers
}

func absenceCellAt(row []string, headers map[string]int, keys ...string) string {
	for _, key := range keys {
		if idx, ok := headers[key]; ok && idx >= 0 && idx < len(row) {
			return strings.TrimSpace(row[idx])
		}
	}
	return ""
}

func absenceAllBlank(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func missedLabel(scheduleType string, date time.Time) string {
	weekday := date.Weekday()

	switch strings.ToLower(scheduleType) {
	case "sharjah_cycle":
		switch weekday {
		case time.Monday:
			return "Sharjah cycle Day 1 (Monday)"
		case time.Tuesday:
			return "Sharjah cycle Day 2 (Tuesday)"
		case time.Wednesday:
			return "Sharjah cycle Day 3 (Wednesday)"
		case time.Thursday:
			return "Sharjah cycle Day 4 (Thursday)"
		default:
			return "Sharjah cycle Day 5 (next teaching day)"
		}
	default:
		return weekday.String()
	}
}
