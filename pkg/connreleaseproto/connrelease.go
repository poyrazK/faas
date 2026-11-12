// Package connreleaseproto defines the guest/host wire contract vmmd uses to
// ask guest-init to reset the guest's TCP connections just before it destroys
// the VM (ADR-20261109130000). Destroying a guest sends nothing on its
// sockets, so a database or cache the app was connected to keeps the session
// until its own keepalive gives up, which is two hours on Linux defaults.
// Resetting them first frees those sessions at once.
package connreleaseproto

// MessageType shares the resume listener with types 1 and 3 to 7. The request
// body is empty; the reply is one OK byte followed by the number of sockets
// reset as a 4-byte big-endian count. A guest that predates it answers with a
// single non-zero byte.
const MessageType uint32 = 8

// ReplyBytes is the length of a successful reply.
const ReplyBytes = 5
