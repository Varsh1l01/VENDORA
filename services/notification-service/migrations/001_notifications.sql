-- Audit log of delivered notifications (no message bodies are stored).
CREATE TABLE IF NOT EXISTS notification_log (
    event_id   UUID PRIMARY KEY,
    event_type TEXT NOT NULL,
    user_id    UUID NOT NULL,
    channel    TEXT NOT NULL,
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
