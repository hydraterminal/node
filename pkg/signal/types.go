package signal

// Category represents the type of intelligence signal.
type Category string

const (
	CategoryAircraftMovement Category = "AIRCRAFT_MOVEMENT"
	CategoryForceDeployment  Category = "FORCE_DEPLOYMENT"
	CategoryDiplomatic       Category = "DIPLOMATIC"
	CategoryCyber            Category = "CYBER"
	CategoryNaval            Category = "NAVAL"
	CategoryNuclear          Category = "NUCLEAR"
	CategoryAirspace         Category = "AIRSPACE"
	CategoryCommunications   Category = "COMMUNICATIONS"
	CategorySocialMedia      Category = "SOCIAL_MEDIA"
	CategoryMissileAlert     Category = "MISSILE_ALERT"
	CategoryMarketMovement   Category = "MARKET_MOVEMENT"
	CategoryEarthquake       Category = "EARTHQUAKE"
	CategoryOther            Category = "OTHER"
)

// Source identifies where a signal originated.
type Source string

const (
	SourceADSB       Source = "ADSB"
	SourceAIS        Source = "AIS"
	SourceOREF       Source = "OREF"
	SourceTwitter    Source = "TWITTER"
	SourceTelegram   Source = "TELEGRAM"
	SourcePolymarket Source = "POLYMARKET"
	SourceUSGS       Source = "USGS"
	SourceManual     Source = "MANUAL"
	SourceSystem     Source = "SYSTEM"
)

// Severity levels for signals.
type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
	SeverityLow      Severity = "LOW"
	SeverityInfo     Severity = "INFO"
)
