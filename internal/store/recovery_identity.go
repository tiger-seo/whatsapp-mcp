package store

import "context"

// RecoveryIdentityPresent reports whether a single local device row exists in
// an already-open unsafe store. It does not load credentials, verify the phone
// or authorize identity removal. The result is a point-in-time observation;
// RecoverRunGuard rechecks the identity within its own transaction.
func (s *Store) RecoveryIdentityPresent(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	release, opCtx, err := s.gate.enter(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	var marker, devices int
	err = s.reader.QueryRowContext(opCtx, `SELECT
		(SELECT count(*) FROM unsafe_run WHERE id=1 AND typeof(id)='integer'
		 AND typeof(state)='text' AND state='unsafe' AND typeof(repair_bits)='integer'
		 AND repair_bits BETWEEN 0 AND 31 AND (SELECT count(*) FROM unsafe_run)=1),
		(SELECT count(*) FROM (SELECT 1 FROM whatsmeow_device LIMIT 2))`).Scan(&marker, &devices)
	if err != nil {
		return false, mapDriverError(err)
	}
	if marker != 1 {
		return false, ErrUnsafeRun
	}
	if devices > 1 {
		return false, unsafeStorage("", "multiple_devices", "storage contains more than one saved device identity", "resolve the unexpected device rows before recovery")
	}
	return devices == 1, nil
}
