package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	PublicBaseURL string
	LogOnly       bool
	SMTPAddr      string
	SMTPUsername  string
	SMTPPassword  string
	SMTPFrom      string
	SMSWebhookURL string
	SMSAPIToken   string
}

type job struct {
	ID        uuid.UUID
	Channel   string
	Recipient string
	Template  string
	Payload   []byte
	Attempts  int
}

type Worker struct {
	db       *pgxpool.Pool
	config   Config
	client   *http.Client
	interval time.Duration
}

func NewWorker(db *pgxpool.Pool, config Config, interval time.Duration) *Worker {
	return &Worker{db: db, config: config, interval: interval, client: &http.Client{Timeout: 8 * time.Second}}
}

func (w *Worker) Run(ctx context.Context) {
	w.run(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.run(ctx)
		}
	}
}

func (w *Worker) run(ctx context.Context) {
	for range 20 {
		item, ok, err := w.claim(ctx)
		if err != nil {
			log.Printf("claim notification: %v", err)
			return
		}
		if !ok {
			return
		}
		err = w.deliver(ctx, item)
		if err == nil {
			_, err = w.db.Exec(ctx, `UPDATE notification_jobs SET status='sent',sent_at=now(),locked_until=NULL,
				last_error=NULL,updated_at=now() WHERE id=$1 AND status='processing'`, item.ID)
		} else {
			delay := time.Duration(math.Min(math.Pow(2, float64(item.Attempts-1)), 360)) * time.Minute
			_, updateErr := w.db.Exec(ctx, `UPDATE notification_jobs SET status='failed',locked_until=NULL,last_error=$2,
				next_attempt_at=$3,updated_at=now() WHERE id=$1 AND status='processing'`, item.ID, truncate(err.Error(), 1000), time.Now().Add(delay))
			if updateErr != nil {
				log.Printf("reschedule notification %s: %v", item.ID, updateErr)
			}
		}
		if err != nil {
			log.Printf("deliver %s notification %s: %v", item.Channel, item.ID, err)
		}
	}
}

func (w *Worker) claim(ctx context.Context) (job, bool, error) {
	var item job
	err := w.db.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM notification_jobs WHERE status IN ('pending','failed') AND next_attempt_at<=now()
		AND (locked_until IS NULL OR locked_until<now()) ORDER BY next_attempt_at,created_at
		FOR UPDATE SKIP LOCKED LIMIT 1
	), claimed AS (
		UPDATE notification_jobs n SET status='processing',attempts=attempts+1,
		locked_until=now()+interval '30 seconds',updated_at=now() FROM candidate c WHERE n.id=c.id
		RETURNING n.id,n.channel,n.recipient,n.template,n.payload,n.attempts)
	SELECT id,channel,recipient,template,payload,attempts FROM claimed`).Scan(
		&item.ID, &item.Channel, &item.Recipient, &item.Template, &item.Payload, &item.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return job{}, false, nil
	}
	return item, err == nil, err
}

func (w *Worker) deliver(ctx context.Context, item job) error {
	subject, body, err := w.render(item)
	if err != nil {
		return err
	}
	if w.config.LogOnly {
		log.Printf("notification log-only channel=%s recipient=%s subject=%q body=%q", item.Channel, item.Recipient, subject, body)
		return nil
	}
	switch item.Channel {
	case "email":
		if w.config.SMTPAddr == "" || w.config.SMTPFrom == "" {
			return errors.New("SMTP delivery is not configured")
		}
		var auth smtp.Auth
		if w.config.SMTPUsername != "" {
			host := strings.Split(w.config.SMTPAddr, ":")[0]
			auth = smtp.PlainAuth("", w.config.SMTPUsername, w.config.SMTPPassword, host)
		}
		message := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
			w.config.SMTPFrom, item.Recipient, subject, body))
		return smtp.SendMail(w.config.SMTPAddr, auth, w.config.SMTPFrom, []string{item.Recipient}, message)
	case "sms":
		if w.config.SMSWebhookURL == "" {
			return errors.New("SMS delivery is not configured")
		}
		payload, _ := json.Marshal(map[string]string{"to": item.Recipient, "message": body})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.config.SMSWebhookURL, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if w.config.SMSAPIToken != "" {
			req.Header.Set("Authorization", "Bearer "+w.config.SMSAPIToken)
		}
		resp, err := w.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("SMS provider returned HTTP %d", resp.StatusCode)
		}
		return nil
	default:
		return errors.New("unsupported notification channel")
	}
}

func (w *Worker) render(item job) (string, string, error) {
	var payload map[string]string
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return "", "", err
	}
	switch item.Template {
	case "password_reset":
		return "Reset your Cinema Booking password", "Use this link within one hour to reset your password: " + payload["reset_url"], nil
	case "booking_confirmed":
		url := strings.TrimRight(w.config.PublicBaseURL, "/") + "/#bookings"
		return "Your cinema booking is confirmed", "Booking " + payload["order_id"] + " is confirmed. View and download your ticket: " + url, nil
	case "refund_completed":
		return "Your cinema booking refund is complete", "The refund for booking " + payload["order_id"] + " has been processed. Amount: INR " + formatPaise(payload["amount_paise"]), nil
	case "email_verification":
		return "Verify your Cinema Booking email", "Verify this email address within 24 hours: " + payload["verify_url"], nil
	case "phone_verification":
		return "Cinema Booking verification code", "Your Cinema Booking verification code is " + payload["code"] + ". It expires in 10 minutes.", nil
	default:
		return "", "", errors.New("unknown notification template")
	}
}

func formatPaise(value string) string {
	if len(value) < 3 {
		value = strings.Repeat("0", 3-len(value)) + value
	}
	return value[:len(value)-2] + "." + value[len(value)-2:]
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
