package ais

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/source"
)

const wsURL = "wss://stream.aisstream.io/v0/stream"

func init() {
	source.Register("ais", func() source.Source { return &Source{} })
}

type Source struct {
	cfg           config.SourceConfig
	apiKey        string
	conn          *websocket.Conn
	logger        *slog.Logger
	flushInterval time.Duration

	// Buffer for flushing
	mu      sync.Mutex
	buffer  []source.NormalizedVessel
}

func (s *Source) Name() string                 { return "ais" }
func (s *Source) Type() source.SourceType      { return source.SourceTypeStream }
func (s *Source) Interval() time.Duration      { return 0 }
func (s *Source) RequiredCredentials() []string { return []string{"AISSTREAM_API_KEY"} }

func (s *Source) Init(cfg config.SourceConfig) error {
	s.cfg = cfg
	s.apiKey = config.GetSourceAPIKey(cfg.APIKeyEnv)
	if s.apiKey == "" {
		s.apiKey = config.GetSourceAPIKey("AISSTREAM_API_KEY")
	}
	s.logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	s.flushInterval = cfg.FlushInterval
	if s.flushInterval == 0 {
		s.flushInterval = 60 * time.Second
	}
	return nil
}

func (s *Source) Stop() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *Source) Start(ctx context.Context, out chan<- source.CollectedBatch) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect AIS: %w", err)
	}
	s.conn = conn

	// Subscribe
	sub := map[string]any{
		"APIKey":             s.apiKey,
		"BoundingBoxes":      [][][]float64{{{-90, -180}, {90, 180}}},
		"FilterMessageTypes": []string{"PositionReport", "ShipStaticData"},
	}
	if err := conn.WriteJSON(sub); err != nil {
		conn.Close()
		return fmt.Errorf("subscribe AIS: %w", err)
	}

	s.logger.Info("connected to AIS stream")

	// Start flush ticker
	go s.flushLoop(ctx, out)

	// Read messages
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read AIS: %w", err)
		}

		var msg aisMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		vessel := s.processMessage(msg)
		if vessel != nil {
			s.mu.Lock()
			s.buffer = append(s.buffer, *vessel)
			s.mu.Unlock()
		}
	}
}

func (s *Source) flushLoop(ctx context.Context, out chan<- source.CollectedBatch) {
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if len(s.buffer) == 0 {
				s.mu.Unlock()
				continue
			}
			vessels := s.buffer
			s.buffer = nil
			s.mu.Unlock()

			out <- source.CollectedBatch{
				Source:      "ais",
				Vessels:     vessels,
				CollectedAt: time.Now(),
			}
		}
	}
}

func (s *Source) processMessage(msg aisMessage) *source.NormalizedVessel {
	switch msg.MessageType {
	case "PositionReport":
		return s.processPosition(msg)
	case "ShipStaticData":
		return s.processStatic(msg)
	}
	return nil
}

func (s *Source) processPosition(msg aisMessage) *source.NormalizedVessel {
	meta := msg.MetaData
	mmsi := meta.MMSIString
	if mmsi == "" {
		return nil
	}

	pos := msg.Message.PositionReport
	if pos == nil {
		return nil
	}

	// Only process if we can determine it's military or tanker from MMSI
	// Full classification happens when we get ShipStaticData
	return &source.NormalizedVessel{
		MMSI:      mmsi,
		Name:      meta.ShipName,
		Latitude:  pos.Latitude,
		Longitude: pos.Longitude,
		Speed:     pos.Sog,
		Course:    pos.Cog,
		Heading:   pos.TrueHeading,
		NavStatus: pos.NavigationalStatus,
		Flag:      flagFromMMSI(mmsi),
	}
}

func (s *Source) processStatic(msg aisMessage) *source.NormalizedVessel {
	meta := msg.MetaData
	mmsi := meta.MMSIString
	if mmsi == "" {
		return nil
	}

	static := msg.Message.ShipStaticData
	if static == nil {
		return nil
	}

	category := classifyShipType(static.Type)
	if category == "" {
		return nil // Not military or tanker
	}

	return &source.NormalizedVessel{
		MMSI:        mmsi,
		Name:        static.Name,
		CallSign:    static.CallSign,
		IMO:         fmt.Sprintf("%d", static.ImoNumber),
		ShipType:    static.Type,
		Category:    category,
		Destination: static.Destination,
		Flag:        flagFromMMSI(mmsi),
		Latitude:    meta.Latitude,
		Longitude:   meta.Longitude,
	}
}

func classifyShipType(shipType int) string {
	if shipType == 35 {
		return "MILITARY"
	}
	if shipType >= 80 && shipType <= 89 {
		return "TANKER"
	}
	return "" // Not tracked
}

func flagFromMMSI(mmsi string) string {
	if len(mmsi) < 3 {
		return ""
	}
	mid := mmsi[:3]
	if cc, ok := midToCountry[mid]; ok {
		return cc
	}
	return ""
}

// Maritime Identification Digits → ISO country code (subset)
var midToCountry = map[string]string{
	"201": "AL", "202": "AD", "203": "AT", "204": "PT", "205": "BE",
	"206": "BY", "207": "BG", "208": "VA", "209": "CY", "210": "CY",
	"211": "DE", "212": "CY", "213": "GE", "214": "MD", "215": "MT",
	"216": "AM", "218": "DE", "219": "DK", "220": "DK", "224": "ES",
	"225": "ES", "226": "FR", "227": "FR", "228": "FR", "229": "MT",
	"230": "FI", "231": "FO", "232": "GB", "233": "GB", "234": "GB",
	"235": "GB", "236": "GI", "237": "GR", "238": "HR", "239": "GR",
	"240": "GR", "241": "GR", "242": "MA", "243": "HU", "244": "NL",
	"245": "NL", "246": "NL", "247": "IT", "248": "MT", "249": "MT",
	"250": "IE", "251": "IS", "252": "LI", "253": "LU", "254": "MC",
	"255": "PT", "256": "MT", "257": "NO", "258": "NO", "259": "NO",
	"261": "PL", "262": "ME", "263": "PT", "264": "RO", "265": "SE",
	"266": "SE", "267": "SK", "268": "SM", "269": "CH", "270": "CZ",
	"271": "TR", "272": "UA", "273": "RU", "274": "MK", "275": "LV",
	"276": "EE", "277": "LT", "278": "SI", "279": "RS",
	"301": "AI", "303": "US", "304": "AG", "305": "AG", "306": "CW",
	"307": "AW", "308": "BS", "309": "BS", "310": "BM", "311": "BS",
	"312": "BZ", "314": "BB", "316": "CA", "319": "KY",
	"323": "CU", "325": "DM", "327": "DO", "329": "GP", "330": "GD",
	"331": "GL", "332": "GT", "334": "HN", "336": "HT", "338": "US",
	"339": "JM", "341": "KN", "343": "LC", "345": "MX", "347": "MQ",
	"348": "MS", "350": "NI", "351": "PA", "352": "PA", "353": "PA",
	"354": "PA", "355": "PA", "356": "PA", "357": "PA", "358": "PR",
	"359": "SV", "361": "PM", "362": "TT", "364": "TC", "366": "US",
	"367": "US", "368": "US", "369": "US", "370": "PA", "371": "PA",
	"372": "PA", "373": "PA", "374": "PA", "375": "VC", "376": "VC",
	"377": "VC",
	"401": "AF", "403": "SA", "405": "BD", "408": "BH", "410": "BT",
	"412": "CN", "413": "CN", "414": "CN", "416": "TW", "417": "LK",
	"419": "IN", "422": "IR", "423": "AZ", "425": "IQ", "428": "IL",
	"431": "JP", "432": "JP", "434": "TM", "436": "KZ", "437": "UZ",
	"438": "JO", "440": "KR", "441": "KR", "443": "PS", "445": "KP",
	"447": "KW", "450": "LB", "451": "KG", "453": "MO", "455": "MV",
	"457": "MN", "459": "NP", "461": "OM", "463": "PK", "466": "QA",
	"468": "SY", "470": "AE", "472": "TJ", "473": "YE", "475": "TH",
	"477": "HK", "478": "BA",
	"501": "FR", "503": "AU", "506": "MM", "508": "BN", "510": "FM",
	"511": "PW", "512": "NZ", "514": "KH", "515": "KH", "516": "AU",
	"518": "CK", "520": "FJ", "523": "CX", "525": "ID", "529": "KI",
	"531": "LA", "533": "MY", "536": "MP", "538": "MH", "540": "NC",
	"542": "NU", "544": "NR", "546": "FR", "548": "PH", "553": "PG",
	"555": "PN", "557": "SB", "559": "AS", "561": "WS", "563": "SG",
	"564": "SG", "565": "SG", "566": "SG", "567": "TH", "570": "TO",
	"572": "TV", "574": "VN", "576": "VU", "577": "VU", "578": "WF",
}

// AIS message types

type aisMessage struct {
	MessageType string   `json:"MessageType"`
	MetaData    aisMeta  `json:"MetaData"`
	Message     aisData  `json:"Message"`
}

type aisMeta struct {
	MMSI       int     `json:"MMSI"`
	MMSIString string  `json:"MMSI_String"`
	ShipName   string  `json:"ShipName"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	TimeUTC    string  `json:"time_utc"`
}

type aisData struct {
	PositionReport *aisPositionReport `json:"PositionReport,omitempty"`
	ShipStaticData *aisShipStatic     `json:"ShipStaticData,omitempty"`
}

type aisPositionReport struct {
	Latitude            float64 `json:"Latitude"`
	Longitude           float64 `json:"Longitude"`
	Sog                 float64 `json:"Sog"`
	Cog                 float64 `json:"Cog"`
	TrueHeading         float64 `json:"TrueHeading"`
	NavigationalStatus  int     `json:"NavigationalStatus"`
	RateOfTurn          float64 `json:"RateOfTurn"`
}

type aisShipStatic struct {
	Name                string  `json:"Name"`
	CallSign            string  `json:"CallSign"`
	ImoNumber           int     `json:"ImoNumber"`
	Destination         string  `json:"Destination"`
	Type                int     `json:"Type"`
	MaximumStaticDraught float64 `json:"MaximumStaticDraught"`
}
