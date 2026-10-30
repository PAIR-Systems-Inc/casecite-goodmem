ALTER TABLE cc_users ADD COLUMN google_subject text;
CREATE UNIQUE INDEX cc_users_google_subject ON cc_users(google_subject) WHERE google_subject IS NOT NULL;
