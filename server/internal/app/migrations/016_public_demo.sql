-- Demo identities have no passwords. Invitations and sessions are bearer
-- credentials; persist only their SHA-256 hashes. Reissuing an invitation
-- retains the same private owner and lifetime upload count.
CREATE TABLE cc_demo_guests (
    user_id text PRIMARY KEY REFERENCES cc_users(id) ON DELETE CASCADE,
    invite_hash text NOT NULL UNIQUE,
    session_hash text UNIQUE,
    expires_at timestamptz NOT NULL,
    redeemed_at timestamptz,
    revoked_at timestamptz,
    uploads_used integer NOT NULL DEFAULT 0 CHECK (uploads_used BETWEEN 0 AND 10)
);

CREATE FUNCTION cc_demo_reserve_upload() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM cc_demo_guests WHERE user_id=NEW.owner_id) THEN
        UPDATE cc_demo_guests SET uploads_used=uploads_used+1
        WHERE user_id=NEW.owner_id AND uploads_used<10
          AND expires_at>now() AND revoked_at IS NULL AND redeemed_at IS NOT NULL;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'Demo upload limit reached or invitation expired'
                USING ERRCODE='P0001', CONSTRAINT='cc_demo_upload_limit';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- AFTER INSERT does not charge rejected files or ON CONFLICT retries. The
-- counter and document row commit together. Concurrent uploads serialize on
-- the guest row; failed upstream ingestion retains its slot for reconciliation.
CREATE TRIGGER cc_demo_upload_limit AFTER INSERT ON cc_objects
    FOR EACH ROW WHEN (NEW.kind='document') EXECUTE FUNCTION cc_demo_reserve_upload();
