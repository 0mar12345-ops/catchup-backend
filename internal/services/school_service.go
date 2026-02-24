package services

import (
	"context"
	"errors"
	"time"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var (
	ErrInvalidSchoolID = errors.New("invalid school id")
	ErrSchoolNotFound  = errors.New("school not found")
)

type CreateSchoolInput struct {
	Name     string
	Code     string
	Domain   string
	Timezone string
	IsActive bool
}

type UpdateSchoolInput struct {
	Name     *string
	Code     *string
	Domain   *string
	Timezone *string
	IsActive *bool
}

type SchoolService struct {
	collection *mongo.Collection
}

func NewSchoolService(client *mongo.Client, dbName string) *SchoolService {
	return &SchoolService{
		collection: client.Database(dbName).Collection("schools"),
	}
}

func (s *SchoolService) CreateSchool(ctx context.Context, input CreateSchoolInput) (*models.School, error) {
	now := time.Now().UTC()

	school := &models.School{
		Name:      input.Name,
		Code:      input.Code,
		Domain:    input.Domain,
		Timezone:  input.Timezone,
		IsActive:  input.IsActive,
		CreatedAt: now,
		UpdatedAt: now,
	}

	result, err := s.collection.InsertOne(ctx, school)
	if err != nil {
		return nil, err
	}

	if oid, ok := result.InsertedID.(bson.ObjectID); ok {
		school.ID = oid
	}

	return school, nil
}

func (s *SchoolService) ListSchools(ctx context.Context) ([]models.School, error) {
	cursor, err := s.collection.Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var schools []models.School
	if err := cursor.All(ctx, &schools); err != nil {
		return nil, err
	}

	return schools, nil
}

func (s *SchoolService) GetSchoolByID(ctx context.Context, id string) (*models.School, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, ErrInvalidSchoolID
	}

	var school models.School
	if err := s.collection.FindOne(ctx, bson.M{"_id": oid}).Decode(&school); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrSchoolNotFound
		}
		return nil, err
	}

	return &school, nil
}

func (s *SchoolService) UpdateSchool(ctx context.Context, id string, input UpdateSchoolInput) (*models.School, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, ErrInvalidSchoolID
	}

	set := bson.M{
		"updated_at": time.Now().UTC(),
	}

	if input.Name != nil {
		set["name"] = *input.Name
	}
	if input.Code != nil {
		set["code"] = *input.Code
	}
	if input.Domain != nil {
		set["domain"] = *input.Domain
	}
	if input.Timezone != nil {
		set["timezone"] = *input.Timezone
	}
	if input.IsActive != nil {
		set["is_active"] = *input.IsActive
	}

	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": set})
	if err != nil {
		return nil, err
	}

	if result.MatchedCount == 0 {
		return nil, ErrSchoolNotFound
	}

	return s.GetSchoolByID(ctx, id)
}

func (s *SchoolService) DeleteSchool(ctx context.Context, id string) error {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return ErrInvalidSchoolID
	}

	result, err := s.collection.DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		return err
	}

	if result.DeletedCount == 0 {
		return ErrSchoolNotFound
	}

	return nil
}
