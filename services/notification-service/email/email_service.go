package email

import (
	"fmt"
	"log"
	"net/smtp"

	"github.com/ayushdevani/forgecart/pkg/config"
)

type EmailService struct {
	host     string
	port     string
	username string
	password string
	from     string
	enabled  bool
}

func NewEmailService() *EmailService {
	enabled := config.GetEnv("SMTP_ENABLED", "false") == "true"

	return &EmailService{
		host:     config.GetEnv("SMTP_HOST", "smtp.gmail.com"),
		port:     config.GetEnv("SMTP_PORT", "587"),
		username: config.GetEnv("SMTP_USERNAME", ""),
		password: config.GetEnv("SMTP_PASSWORD", ""),
		from:     config.GetEnv("SMTP_FROM", "noreply@forgecart.com"),
		enabled:  enabled,
	}
}

func (s *EmailService) Send(to, subject, body string) error {
	if !s.enabled {
		// Log instead of sending when SMTP is disabled
		log.Printf("[EMAIL] To: %s | Subject: %s | Body: %s", to, subject, body)
		return nil
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s",
		s.from, to, subject, body)

	auth := smtp.PlainAuth("", s.username, s.password, s.host)
	addr := fmt.Sprintf("%s:%s", s.host, s.port)

	return smtp.SendMail(addr, auth, s.from, []string{to}, []byte(msg))
}

// Email Templates

func (s *EmailService) SendWelcome(email, firstName string) error {
	subject := "Welcome to ForgeCart!"
	body := fmt.Sprintf(`Hi %s,
Welcome to ForgeCart! We're excited to have you.
Start shopping now and discover amazing products.
Best,
The ForgeCart Team`, firstName)

	return s.Send(email, subject, body)
}

func (s *EmailService) SendOrderConfirmation(email, orderID string, total float64) error {
	subject := fmt.Sprintf("Order Confirmed #%s", orderID)
	body := fmt.Sprintf(`Your order has been confirmed!
Order ID: %s
Total: $%.2f
We'll notify you when your order ships.
Thank you for shopping with ForgeCart!`, orderID, total)

	return s.Send(email, subject, body)
}

func (s *EmailService) SendOrderStatusUpdate(email, orderID, status string) error {
	subject := fmt.Sprintf("Order Update #%s", orderID)
	body := fmt.Sprintf(`Your order status has been updated.
Order ID: %s
New Status: %s
Track your order in your account.
The ForgeCart Team`, orderID, status)

	return s.Send(email, subject, body)
}

func (s *EmailService) SendPaymentReceipt(email, paymentID, orderID string, amount float64) error {
	subject := "Payment Receipt - ForgeCart"
	body := fmt.Sprintf(`Payment received successfully!
Payment ID: %s
Order ID: %s
Amount: $%.2f
Thank you for your purchase!

The ForgeCart Team`, paymentID, orderID, amount)

	return s.Send(email, subject, body)
}
