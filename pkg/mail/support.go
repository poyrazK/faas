package mail

import "fmt"

// SupportUpdateBody deliberately accepts no customer text or ticket subject.
// The authenticated conversation is the sole source of message content.
func SupportUpdateBody(kind, ticketID, link string) (string, string) {
	subject, summary := "Gregale support update", "There is an update to your Gregale support conversation."
	switch kind {
	case "staff_reply":
		summary = "Gregale support has replied to your conversation."
	case "customer_reply":
		subject = "Gregale support: customer reply"
		summary = "A customer has added a message to a support conversation."
	case "ticket_created":
		subject = "Gregale support: new conversation"
		summary = "A customer has opened a support conversation."
	case "assigned":
		subject = "Gregale support: ticket assigned to you"
		summary = "A support conversation has been assigned to you."
	}
	return subject, fmt.Sprintf("%s\n\nTicket: %s\nOpen the conversation in Gregale:\n%s\n\nSign in to read the update. Replies to this email are not monitored.\n", summary, ticketID, link)
}
