package wss

// Client connects to a Mind from a Face or Hand.
type Client struct{}

// NewClient creates a new WSS client.
func NewClient(serverURL string) *Client {
	return &Client{}
}

// Connect establishes a WSS connection.
func (c *Client) Connect() error {
	return nil
}
