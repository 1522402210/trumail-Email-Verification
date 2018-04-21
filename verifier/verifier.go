package verifier

import (
	"encoding/xml"
	"time"

	"github.com/sdwolfe32/httpclient"
)

// Verifier contains all dependencies needed to perform educated email
// verification lookups
type Verifier struct {
	client               *httpclient.Client
	hostname, sourceAddr string
	disp                 *Disposabler
}

// NewVerifier generates a new httpclient.Client using the passed timeout
// and then returns a new Verifier reference that will be used to Verify
// email addresses
func NewVerifier(hostname, sourceAddr string) *Verifier {
	client := httpclient.New(time.Second*30, nil)
	return &Verifier{client, hostname, sourceAddr, NewDisposabler(client)}
}

// Lookup contains all output data for an email verification Lookup
type Lookup struct {
	XMLName xml.Name `json:"-" xml:"lookup"`
	Address
	Deliverable bool `json:"deliverable" xml:"deliverable"`
	FullInbox   bool `json:"fullInbox" xml:"fullInbox"`
	HostExists  bool `json:"hostExists" xml:"hostExists"`
	CatchAll    bool `json:"catchAll" xml:"catchAll"`
	Disposable  bool `json:"disposable" xml:"disposable"`
	Gravatar    bool `json:"gravatar" xml:"gravatar"`
}

// VerifyAddressTimeout performs an email verification, failing with an ErrTimeout
// if a valid Lookup isn't produced within the timeout passed
func (v *Verifier) VerifyAddressTimeout(address *Address, timeout time.Duration) (*Lookup, error) {
	ch := make(chan interface{}, 1)

	// Create a goroutine that will attempt to connect to the SMTP server
	go func() {
		d, err := v.VerifyAddress(address)
		if err != nil {
			ch <- err
		} else {
			ch <- d
		}
	}()

	// Block until a response is produced or timeout
	select {
	case res := <-ch:
		switch r := res.(type) {
		case *Lookup:
			return r, nil
		case error:
			return nil, r
		default:
			return nil, newLookupError(ErrUnexpectedResponse, ErrUnexpectedResponse)
		}
	case <-time.After(timeout):
		return nil, newLookupError(ErrTimeout, ErrTimeout)
	}
}

// VerifyEmail parses the passed email address and verifies it's
// deliverability, returning any errors that are encountered
func (v *Verifier) VerifyEmail(email string) (*Lookup, error) {
	// First parse the email string passed
	a, err := ParseAddress(email)
	if err != nil {
		return nil, newLookupError(ErrEmailParseFailure, ErrEmailParseFailure)
	}

	// Perform the verification with the parsed address
	return v.VerifyAddress(a)
}

// VerifyAddress performs an email verification on the passed
// Address
func (v *Verifier) VerifyAddress(address *Address) (*Lookup, error) {
	// Declare the lookup to be populated and populate
	// all inital field values
	l := &Lookup{
		Address:    *address,
		HostExists: true,
		Disposable: v.disp.IsDisposable(address.Domain),
		Gravatar:   v.HasGravatar(address),
	}

	// Attempt to form an SMTP Connection
	del, err := NewDeliverabler(address.Domain, v.hostname, v.sourceAddr)
	if err != nil {
		le := parseRCPTErr(err)
		if le != nil {
			if le.Message == ErrNoSuchHost {
				l.HostExists = false
			}
		}
		return nil, parseSTDErr(err)
	}
	defer del.Close() // Defer close the SMTP connection

	// Retrieve the catchall status
	if del.HasCatchAll(3) {
		l.CatchAll = true
		l.Deliverable = true
	}

	// Perform the main address verification if not a catchall server
	if !l.CatchAll {
		if err := del.IsDeliverable(address.Address, 3); err != nil {
			le := parseRCPTErr(err)
			if le != nil {
				if le.Message == ErrFullInbox {
					l.FullInbox = true // Set FullInbox and move on
				} else {
					return nil, le // Return if it's a legit error
				}
			}
		} else {
			l.Deliverable = true
		}
	}
	return l, nil
}
