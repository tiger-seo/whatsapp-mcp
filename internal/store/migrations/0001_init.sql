CREATE TABLE mcp_version (
    version INTEGER NOT NULL
) STRICT;

INSERT INTO mcp_version (version) VALUES (1);

CREATE TABLE chats (
    jid               TEXT    NOT NULL PRIMARY KEY,
    phone             TEXT    NOT NULL DEFAULT '',
    name              TEXT    NOT NULL DEFAULT '',
    name_fold         TEXT    NOT NULL DEFAULT '',
    name_quality      INTEGER NOT NULL DEFAULT 0 CHECK (name_quality IN (0, 1, 2, 3)),
    name_time         INTEGER,
    last_message_time INTEGER
) STRICT;

CREATE TABLE messages (
    chat_jid          TEXT    NOT NULL REFERENCES chats (jid),
    id                TEXT    NOT NULL,
    sender_jid        TEXT    NOT NULL,
    sender_phone      TEXT    NOT NULL DEFAULT '',
    time              INTEGER NOT NULL,
    from_me           INTEGER NOT NULL CHECK (from_me IN (0, 1)),
    text              TEXT    NOT NULL DEFAULT '',
    text_fold         TEXT    NOT NULL DEFAULT '',
    caption           TEXT    NOT NULL DEFAULT '',
    caption_fold      TEXT    NOT NULL DEFAULT '',
    media_type        TEXT    NOT NULL DEFAULT '' CHECK (media_type IN ('', 'image', 'video', 'audio', 'document')),
    media_url         TEXT,
    media_direct_path TEXT,
    media_key         BLOB,
    file_sha256       BLOB,
    file_enc_sha256   BLOB,
    file_length       INTEGER,
    mime_type         TEXT,
    file_name         TEXT,
    PRIMARY KEY (chat_jid, id)
) STRICT;

CREATE INDEX chats_last_activity ON chats ((last_message_time IS NULL), last_message_time DESC, jid);
CREATE INDEX chats_name ON chats ((name_quality = 0), name_fold, jid);
CREATE INDEX chats_phone ON chats (phone);

CREATE INDEX messages_chat_time ON messages (chat_jid, time DESC, id DESC);
CREATE INDEX messages_sender_time ON messages (sender_phone, time DESC, chat_jid DESC, id DESC);
CREATE INDEX messages_sender_jid ON messages (sender_jid, time DESC, chat_jid DESC, id DESC);
CREATE INDEX messages_time ON messages (time DESC, chat_jid DESC, id DESC);
CREATE INDEX messages_id ON messages (id);
CREATE INDEX messages_chat_sender_phone ON messages (chat_jid, sender_phone, time DESC, id DESC);
CREATE INDEX messages_chat_sender_jid ON messages (chat_jid, sender_jid, time DESC, id DESC);
