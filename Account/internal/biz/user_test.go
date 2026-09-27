package biz

import (
	"context"
	"errors"
	"io"
	"testing"

	klog "github.com/go-kratos/kratos/v2/log"
)

type profileTestRepo struct {
	UserRepo
	user  User
	saved *User
}

func (r *profileTestRepo) FindByID(context.Context, int64) (*User, error) {
	u := r.user
	return &u, nil
}

func (r *profileTestRepo) Update(_ context.Context, u *User) error {
	copy := *u
	r.saved = &copy
	return nil
}

func TestModifyUserInfoGender(t *testing.T) {
	value := func(v int32) *int32 { return &v }
	for _, tc := range []struct {
		name    string
		gender  *int32
		want    int32
		wantErr bool
	}{
		{name: "omitted preserves gender", want: GenderFemale},
		{name: "explicit zero resets to private", gender: value(0), want: GenderUnspecified},
		{name: "male", gender: value(1), want: GenderMale},
		{name: "female", gender: value(2), want: GenderFemale},
		{name: "negative rejected", gender: value(-1), wantErr: true},
		{name: "unknown rejected", gender: value(3), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &profileTestRepo{user: User{ID: 67, Gender: GenderFemale}}
			uc := NewUserUseCase(repo, klog.NewStdLogger(io.Discard))
			user, err := uc.ModifyUserInfo(context.Background(), 67, "", tc.gender, "")
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidParam) || repo.saved != nil {
					t.Fatalf("invalid gender: err=%v, saved=%v", err, repo.saved)
				}
				return
			}
			if err != nil || user == nil || repo.saved == nil {
				t.Fatalf("update failed: user=%v, saved=%v, err=%v", user, repo.saved, err)
			}
			if user.Gender != tc.want || repo.saved.Gender != tc.want {
				t.Fatalf("gender=%d, saved=%d, want=%d", user.Gender, repo.saved.Gender, tc.want)
			}
		})
	}
}
