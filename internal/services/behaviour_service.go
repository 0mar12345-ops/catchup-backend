package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	ErrInvalidBehaviourTeacherID = errors.New("invalid teacher id")
	ErrInvalidBehaviourSchoolID  = errors.New("invalid school id")
	ErrInvalidBehaviourCourseID  = errors.New("invalid course id")
	ErrInvalidBehaviourType      = errors.New("type must be 'positive' or 'negative'")
	ErrBehaviourCategoryRequired = errors.New("category is required")
	ErrBehaviourStudentRequired  = errors.New("student_name is required")
)

type BehaviourService struct {
	collection *mongo.Collection
}

func NewBehaviourService(client *mongo.Client, dbName string) *BehaviourService {
	return &BehaviourService{
		collection: client.Database(dbName).Collection("behaviour_logs"),
	}
}

type CreateBehaviourLogInput struct {
	TeacherID    string
	SchoolID     string
	CourseID     string
	CourseName   string
	StudentEmail string
	StudentName  string
	Type         string
	Category     string
	Notes        string
	Date         string // YYYY-MM-DD; defaults to today if blank
}

type BehaviourLogResponse struct {
	ID           string    `json:"id"`
	SchoolID     string    `json:"school_id"`
	TeacherID    string    `json:"teacher_id"`
	CourseID     string    `json:"course_id"`
	CourseName   string    `json:"course_name"`
	StudentEmail string    `json:"student_email"`
	StudentName  string    `json:"student_name"`
	Type         string    `json:"type"`
	Category     string    `json:"category"`
	Notes        string    `json:"notes,omitempty"`
	Date         time.Time `json:"date"`
	CreatedAt    time.Time `json:"created_at"`
}

func (s *BehaviourService) CreateBehaviourLog(ctx context.Context, input CreateBehaviourLogInput) (*BehaviourLogResponse, error) {
	teacherOID, err := bson.ObjectIDFromHex(input.TeacherID)
	if err != nil {
		return nil, ErrInvalidBehaviourTeacherID
	}

	schoolOID, err := bson.ObjectIDFromHex(input.SchoolID)
	if err != nil {
		return nil, ErrInvalidBehaviourSchoolID
	}

	courseOID, err := bson.ObjectIDFromHex(input.CourseID)
	if err != nil {
		return nil, ErrInvalidBehaviourCourseID
	}

	bType := models.BehaviourType(strings.TrimSpace(input.Type))
	if bType != models.BehaviourTypePositive && bType != models.BehaviourTypeNegative {
		return nil, ErrInvalidBehaviourType
	}

	if strings.TrimSpace(input.Category) == "" {
		return nil, ErrBehaviourCategoryRequired
	}

	if strings.TrimSpace(input.StudentName) == "" {
		return nil, ErrBehaviourStudentRequired
	}

	logDate := time.Now().UTC().Truncate(24 * time.Hour)
	if strings.TrimSpace(input.Date) != "" {
		if parsed, err := time.Parse("2006-01-02", input.Date); err == nil {
			logDate = parsed.UTC()
		}
	}

	now := time.Now().UTC()
	doc := models.BehaviourLog{
		ID:           bson.NewObjectID(),
		SchoolID:     schoolOID,
		TeacherID:    teacherOID,
		CourseID:     courseOID,
		CourseName:   strings.TrimSpace(input.CourseName),
		StudentEmail: strings.TrimSpace(input.StudentEmail),
		StudentName:  strings.TrimSpace(input.StudentName),
		Type:         bType,
		Category:     strings.TrimSpace(input.Category),
		Notes:        strings.TrimSpace(input.Notes),
		Date:         logDate,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if _, err := s.collection.InsertOne(ctx, doc); err != nil {
		return nil, err
	}

	return toResponse(doc), nil
}

type GetBehaviourLogsInput struct {
	TeacherID string
	SchoolID  string
	Type      string // optional filter: "positive" or "negative"
}

type BehaviourLogsListResponse struct {
	Logs  []BehaviourLogResponse `json:"logs"`
	Total int                    `json:"total"`
}

func (s *BehaviourService) GetBehaviourLogs(ctx context.Context, input GetBehaviourLogsInput) (*BehaviourLogsListResponse, error) {
	teacherOID, err := bson.ObjectIDFromHex(input.TeacherID)
	if err != nil {
		return nil, ErrInvalidBehaviourTeacherID
	}

	schoolOID, err := bson.ObjectIDFromHex(input.SchoolID)
	if err != nil {
		return nil, ErrInvalidBehaviourSchoolID
	}

	filter := bson.M{
		"teacher_id": teacherOID,
		"school_id":  schoolOID,
	}

	if t := strings.TrimSpace(input.Type); t == string(models.BehaviourTypePositive) || t == string(models.BehaviourTypeNegative) {
		filter["type"] = models.BehaviourType(t)
	}

	opts := options.Find().SetSort(bson.D{{Key: "date", Value: -1}, {Key: "created_at", Value: -1}})

	cursor, err := s.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []models.BehaviourLog
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	resp := &BehaviourLogsListResponse{
		Logs:  make([]BehaviourLogResponse, 0, len(docs)),
		Total: len(docs),
	}
	for _, d := range docs {
		resp.Logs = append(resp.Logs, *toResponse(d))
	}

	return resp, nil
}

func (s *BehaviourService) GetBehaviourLogsByCourse(ctx context.Context, teacherID, schoolID, courseID string) (*BehaviourLogsListResponse, error) {
	teacherOID, err := bson.ObjectIDFromHex(teacherID)
	if err != nil {
		return nil, ErrInvalidBehaviourTeacherID
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, ErrInvalidBehaviourSchoolID
	}

	courseOID, err := bson.ObjectIDFromHex(courseID)
	if err != nil {
		return nil, ErrInvalidBehaviourCourseID
	}

	filter := bson.M{
		"teacher_id": teacherOID,
		"school_id":  schoolOID,
		"course_id":  courseOID,
	}

	opts := options.Find().SetSort(bson.D{{Key: "date", Value: -1}, {Key: "created_at", Value: -1}})

	cursor, err := s.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []models.BehaviourLog
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	resp := &BehaviourLogsListResponse{
		Logs:  make([]BehaviourLogResponse, 0, len(docs)),
		Total: len(docs),
	}
	for _, d := range docs {
		resp.Logs = append(resp.Logs, *toResponse(d))
	}

	return resp, nil
}

func toResponse(d models.BehaviourLog) *BehaviourLogResponse {
	return &BehaviourLogResponse{
		ID:           d.ID.Hex(),
		SchoolID:     d.SchoolID.Hex(),
		TeacherID:    d.TeacherID.Hex(),
		CourseID:     d.CourseID.Hex(),
		CourseName:   d.CourseName,
		StudentEmail: d.StudentEmail,
		StudentName:  d.StudentName,
		Type:         string(d.Type),
		Category:     d.Category,
		Notes:        d.Notes,
		Date:         d.Date,
		CreatedAt:    d.CreatedAt,
	}
}
