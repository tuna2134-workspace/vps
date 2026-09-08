-- 0016_billing_suspension.sql
-- Track subscription suspension (payment overdue) and the termination
-- deadline. When a payment fails the subscription is marked suspended and the
-- user's VMs are shut down; if the debt is not settled within the grace
-- period (terminate_at) the VMs are automatically terminated.

ALTER TABLE subscriptions
    ADD COLUMN suspended_at TIMESTAMPTZ,
    ADD COLUMN terminate_at  TIMESTAMPTZ;