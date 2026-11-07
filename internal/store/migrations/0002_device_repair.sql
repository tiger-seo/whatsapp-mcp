CREATE TABLE device_repair (
    device_jid    TEXT    NOT NULL REFERENCES whatsmeow_device (jid) ON DELETE CASCADE
                          CHECK (length(CAST(device_jid AS BLOB)) BETWEEN 1 AND 256),
    collection    TEXT    NOT NULL CHECK (collection IN ('critical_block', 'critical_unblock_low', 'regular_high', 'regular', 'regular_low')),
    revision      INTEGER NOT NULL CHECK (revision > 0),
    target_version BLOB CHECK (target_version IS NULL OR length(target_version) = 8),
    repair_ticket  BLOB CHECK (repair_ticket IS NULL OR length(repair_ticket) = 16),
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (device_jid, collection)
) STRICT;
