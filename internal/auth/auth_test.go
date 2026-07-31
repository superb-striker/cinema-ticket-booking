package auth

import (
	"regexp"
	"testing"
)

func TestVerificationCodeIsSixDigits(t *testing.T) {
	for range 100 {
		code, err := verificationCode()
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(code) {
			t.Fatalf("invalid verification code %q", code)
		}
	}
}

func TestVerificationHashBindsChannelTargetAndCode(t *testing.T) {
	service := NewService(nil, 0, false, "https://cinema.example", "a-separate-verification-secret-value")
	base := service.verificationHash("sms", "+919876543210", "123456")
	for _, changed := range [][]byte{
		service.verificationHash("email", "+919876543210", "123456"),
		service.verificationHash("sms", "+919876543211", "123456"),
		service.verificationHash("sms", "+919876543210", "123457"),
	} {
		if string(base) == string(changed) {
			t.Fatal("verification hash did not bind all challenge fields")
		}
	}
}
