package apptaskproto

// RehearsalVersion deliberately differs from batch version 1 and interactive
// version 2. Older guest-init binaries reject it before dispatch, even if the
// deployment happens to contain an executable with the reserved builtin name.
const RehearsalVersion uint16 = 3
