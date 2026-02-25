package services

import (
	"context"
	"errors"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var (
	ErrInvalidCourseUserID   = errors.New("invalid auth user id")
	ErrInvalidCourseSchoolID = errors.New("invalid auth school id")
)

type CourseDashboardItem struct {
	ID            bson.ObjectID `json:"id"`
	Name          string        `json:"name"`
	Section       string        `json:"section,omitempty"`
	Subject       string        `json:"subject,omitempty"`
	GradeLevel    string        `json:"grade_level,omitempty"`
	Source        string        `json:"source"`
	IsArchived    bool          `json:"is_archived"`
	TotalStudents int64         `json:"total_students"`
}

type CourseService struct {
	coursesCollection     *mongo.Collection
	enrollmentsCollection *mongo.Collection
}

func NewCourseService(client *mongo.Client, dbName string) *CourseService {
	db := client.Database(dbName)

	return &CourseService{
		coursesCollection:     db.Collection("courses"),
		enrollmentsCollection: db.Collection("enrollments"),
	}
}

func (s *CourseService) ListDashboardCourses(ctx context.Context, userID, schoolID string) ([]CourseDashboardItem, error) {
	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, ErrInvalidCourseUserID
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, ErrInvalidCourseSchoolID
	}

	cursor, err := s.coursesCollection.Find(ctx, bson.M{
		"school_id":  schoolOID,
		"teacher_id": userOID,
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	courses := make([]models.Course, 0)
	if err := cursor.All(ctx, &courses); err != nil {
		return nil, err
	}

	items := make([]CourseDashboardItem, 0, len(courses))
	for _, course := range courses {
		totalStudents, err := s.enrollmentsCollection.CountDocuments(ctx, bson.M{
			"school_id": schoolOID,
			"course_id": course.ID,
			"status":    models.EnrollmentActive,
		})
		if err != nil {
			totalStudents = int64(course.StudentCount)
		}

		items = append(items, CourseDashboardItem{
			ID:            course.ID,
			Name:          course.Name,
			Section:       course.Section,
			Subject:       course.Subject,
			GradeLevel:    course.GradeLevel,
			Source:        string(course.Source),
			IsArchived:    course.IsArchived,
			TotalStudents: totalStudents,
		})
	}

	return items, nil
}
