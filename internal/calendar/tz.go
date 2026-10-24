package calendar

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// windowsZones maps Windows time zone ids (used by Exchange/Graph) to IANA
// zones (CLDR windowsZones.xml, territory "001").
var windowsZones = map[string]string{
	"Dateline Standard Time": "Etc/GMT+12", "UTC-11": "Etc/GMT+11", "Aleutian Standard Time": "America/Adak",
	"Hawaiian Standard Time": "Pacific/Honolulu", "Marquesas Standard Time": "Pacific/Marquesas",
	"Alaskan Standard Time": "America/Anchorage", "UTC-09": "Etc/GMT+9", "Pacific Standard Time (Mexico)": "America/Tijuana",
	"UTC-08": "Etc/GMT+8", "Pacific Standard Time": "America/Los_Angeles", "US Mountain Standard Time": "America/Phoenix",
	"Mountain Standard Time (Mexico)": "America/Mazatlan", "Mountain Standard Time": "America/Denver",
	"Yukon Standard Time": "America/Whitehorse", "Central America Standard Time": "America/Guatemala",
	"Central Standard Time": "America/Chicago", "Easter Island Standard Time": "Pacific/Easter",
	"Central Standard Time (Mexico)": "America/Mexico_City", "Canada Central Standard Time": "America/Regina",
	"SA Pacific Standard Time": "America/Bogota", "Eastern Standard Time (Mexico)": "America/Cancun",
	"Eastern Standard Time": "America/New_York", "Haiti Standard Time": "America/Port-au-Prince",
	"Cuba Standard Time": "America/Havana", "US Eastern Standard Time": "America/Indianapolis",
	"Turks And Caicos Standard Time": "America/Grand_Turk", "Paraguay Standard Time": "America/Asuncion",
	"Atlantic Standard Time": "America/Halifax", "Venezuela Standard Time": "America/Caracas",
	"Central Brazilian Standard Time": "America/Cuiaba", "SA Western Standard Time": "America/La_Paz",
	"Pacific SA Standard Time": "America/Santiago", "Newfoundland Standard Time": "America/St_Johns",
	"Tocantins Standard Time": "America/Araguaina", "E. South America Standard Time": "America/Sao_Paulo",
	"SA Eastern Standard Time": "America/Cayenne", "Argentina Standard Time": "America/Buenos_Aires",
	"Greenland Standard Time": "America/Godthab", "Montevideo Standard Time": "America/Montevideo",
	"Magallanes Standard Time": "America/Punta_Arenas", "Saint Pierre Standard Time": "America/Miquelon",
	"Bahia Standard Time": "America/Bahia", "UTC-02": "Etc/GMT+2", "Mid-Atlantic Standard Time": "Etc/GMT+2",
	"Azores Standard Time": "Atlantic/Azores", "Cape Verde Standard Time": "Atlantic/Cape_Verde", "UTC": "Etc/UTC",
	"Coordinated Universal Time": "Etc/UTC", "GMT Standard Time": "Europe/London", "Greenwich Standard Time": "Atlantic/Reykjavik",
	"Sao Tome Standard Time": "Africa/Sao_Tome", "Morocco Standard Time": "Africa/Casablanca",
	"W. Europe Standard Time": "Europe/Berlin", "Central Europe Standard Time": "Europe/Budapest",
	"Romance Standard Time": "Europe/Paris", "Central European Standard Time": "Europe/Warsaw",
	"W. Central Africa Standard Time": "Africa/Lagos", "Jordan Standard Time": "Asia/Amman",
	"GTB Standard Time": "Europe/Bucharest", "Middle East Standard Time": "Asia/Beirut", "Egypt Standard Time": "Africa/Cairo",
	"E. Europe Standard Time": "Europe/Chisinau", "Syria Standard Time": "Asia/Damascus",
	"West Bank Standard Time": "Asia/Hebron", "South Africa Standard Time": "Africa/Johannesburg",
	"FLE Standard Time": "Europe/Kiev", "Israel Standard Time": "Asia/Jerusalem", "South Sudan Standard Time": "Africa/Juba",
	"Kaliningrad Standard Time": "Europe/Kaliningrad", "Sudan Standard Time": "Africa/Khartoum",
	"Libya Standard Time": "Africa/Tripoli", "Namibia Standard Time": "Africa/Windhoek",
	"Arabic Standard Time": "Asia/Baghdad", "Turkey Standard Time": "Europe/Istanbul", "Arab Standard Time": "Asia/Riyadh",
	"Belarus Standard Time": "Europe/Minsk", "Russian Standard Time": "Europe/Moscow", "E. Africa Standard Time": "Africa/Nairobi",
	"Volgograd Standard Time": "Europe/Volgograd", "Iran Standard Time": "Asia/Tehran", "Arabian Standard Time": "Asia/Dubai",
	"Astrakhan Standard Time": "Europe/Astrakhan", "Azerbaijan Standard Time": "Asia/Baku",
	"Russia Time Zone 3": "Europe/Samara", "Mauritius Standard Time": "Indian/Mauritius",
	"Saratov Standard Time": "Europe/Saratov", "Georgian Standard Time": "Asia/Tbilisi",
	"Caucasus Standard Time": "Asia/Yerevan", "Afghanistan Standard Time": "Asia/Kabul",
	"West Asia Standard Time": "Asia/Tashkent", "Ekaterinburg Standard Time": "Asia/Yekaterinburg",
	"Pakistan Standard Time": "Asia/Karachi", "Qyzylorda Standard Time": "Asia/Qyzylorda",
	"India Standard Time": "Asia/Kolkata", "Sri Lanka Standard Time": "Asia/Colombo", "Nepal Standard Time": "Asia/Kathmandu",
	"Central Asia Standard Time": "Asia/Almaty", "Bangladesh Standard Time": "Asia/Dhaka", "Omsk Standard Time": "Asia/Omsk",
	"Myanmar Standard Time": "Asia/Rangoon", "SE Asia Standard Time": "Asia/Bangkok", "Altai Standard Time": "Asia/Barnaul",
	"W. Mongolia Standard Time": "Asia/Hovd", "North Asia Standard Time": "Asia/Krasnoyarsk",
	"N. Central Asia Standard Time": "Asia/Novosibirsk", "Tomsk Standard Time": "Asia/Tomsk",
	"China Standard Time": "Asia/Shanghai", "North Asia East Standard Time": "Asia/Irkutsk",
	"Singapore Standard Time": "Asia/Singapore", "W. Australia Standard Time": "Australia/Perth",
	"Taipei Standard Time": "Asia/Taipei", "Ulaanbaatar Standard Time": "Asia/Ulaanbaatar",
	"Aus Central W. Standard Time": "Australia/Eucla", "Transbaikal Standard Time": "Asia/Chita",
	"Tokyo Standard Time": "Asia/Tokyo", "North Korea Standard Time": "Asia/Pyongyang", "Korea Standard Time": "Asia/Seoul",
	"Yakutsk Standard Time": "Asia/Yakutsk", "Cen. Australia Standard Time": "Australia/Adelaide",
	"AUS Central Standard Time": "Australia/Darwin", "E. Australia Standard Time": "Australia/Brisbane",
	"AUS Eastern Standard Time": "Australia/Sydney", "West Pacific Standard Time": "Pacific/Port_Moresby",
	"Tasmania Standard Time": "Australia/Hobart", "Vladivostok Standard Time": "Asia/Vladivostok",
	"Lord Howe Standard Time": "Australia/Lord_Howe", "Bougainville Standard Time": "Pacific/Bougainville",
	"Russia Time Zone 10": "Asia/Srednekolymsk", "Magadan Standard Time": "Asia/Magadan",
	"Norfolk Standard Time": "Pacific/Norfolk", "Sakhalin Standard Time": "Asia/Sakhalin",
	"Central Pacific Standard Time": "Pacific/Guadalcanal", "Russia Time Zone 11": "Asia/Kamchatka",
	"New Zealand Standard Time": "Pacific/Auckland", "UTC+12": "Etc/GMT-12", "Fiji Standard Time": "Pacific/Fiji",
	"Chatham Islands Standard Time": "Pacific/Chatham", "UTC+13": "Etc/GMT-13", "Tonga Standard Time": "Pacific/Tongatapu",
	"Samoa Standard Time": "Pacific/Apia", "Line Islands Standard Time": "Pacific/Kiritimati",
}

// IANAZone turns a provider time zone name (IANA, Windows, or a Windows
// display name like "(UTC+01:00) Amsterdam, Berlin, ...") into an IANA
// name that loads, or "" when unknown.
func IANAZone(name string) string {
	if z := knownZone(name); z != "" {
		return z
	}
	return displayZone(name)
}

// knownZone is IANAZone for IANA and Windows ids only.
func knownZone(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if z, ok := windowsZones[name]; ok {
		name = z
	}
	switch {
	case strings.EqualFold(name, "UTC"), name == "Etc/UTC", name == "GMT", name == "Etc/GMT", name == "tzone://Microsoft/Utc":
		return "UTC"
	}
	if _, err := time.LoadLocation(name); err != nil {
		return ""
	}
	return name
}

// displayRe matches a Windows display name: "(UTC+01:00) Amsterdam, Berlin"
// ("(GMT+01:00)" on older Windows, "(UTC)" for offset 0).
var displayRe = regexp.MustCompile(`^\((?:UTC|GMT)(?:([+-])(\d{1,2}):(\d{2}))?\)\s*(.*)$`)

// displayNames maps display names without a city to their zone.
var displayNames = map[string]string{
	"pacific time (us & canada)": "America/Los_Angeles", "mountain time (us & canada)": "America/Denver",
	"central time (us & canada)": "America/Chicago", "eastern time (us & canada)": "America/New_York",
	"atlantic time (canada)": "America/Halifax", "alaska": "America/Anchorage", "hawaii": "Pacific/Honolulu",
	"coordinated universal time": "UTC",
}

var displayCache = struct {
	sync.Mutex
	m map[string]string // display name -> IANA name or ""
}{m: map[string]string{}}

// displayOffset returns the UTC offset (seconds) of a Windows display name.
func displayOffset(name string) (int, bool) {
	m := displayRe.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[2])
	mi, _ := strconv.Atoi(m[3])
	off := h*3600 + mi*60
	if m[1] == "-" {
		off = -off
	}
	return off, true
}

// displayZone finds the zone of a Windows display name: one of its cities
// (Europe/Amsterdam for "Amsterdam, Berlin, ...") whose standard or summer
// offset is the one in the name. "" when none fits.
func displayZone(name string) string {
	name = strings.TrimSpace(name)
	off, ok := displayOffset(name)
	if !ok {
		return ""
	}
	displayCache.Lock()
	z, ok := displayCache.m[name]
	displayCache.Unlock()
	if ok {
		return z
	}
	rest := displayRe.FindStringSubmatch(name)[4]
	fits := func(z string) bool {
		loc, err := time.LoadLocation(z)
		if err != nil {
			return false
		}
		year := time.Now().Year()
		for _, m := range []time.Month{time.January, time.July} {
			if _, o := time.Date(year, m, 1, 12, 0, 0, 0, loc).Zone(); o == off {
				return true
			}
		}
		return false
	}
	found := ""
	if z := displayNames[strings.ToLower(rest)]; z != "" && fits(z) {
		found = z
	}
	for i, city := range strings.Split(rest, ",") {
		city = strings.ReplaceAll(strings.TrimSpace(city), " ", "_")
		if found != "" || i >= 8 || city == "" || strings.ContainsAny(city, "()&/.") {
			continue
		}
		for _, region := range []string{"Europe", "America", "Asia", "Africa", "Australia", "Pacific", "Atlantic", "Indian"} {
			if fits(region + "/" + city) {
				found = region + "/" + city
				break
			}
		}
	}
	displayCache.Lock()
	if len(displayCache.m) < 256 {
		displayCache.m[name] = found
	}
	displayCache.Unlock()
	return found
}

// extraWindows maps IANA zones that aren't a territory-"001" default to
// their Windows id (CLDR windowsZones, common cities).
var extraWindows = map[string]string{
	"Europe/Amsterdam": "W. Europe Standard Time", "Europe/Rome": "W. Europe Standard Time", "Europe/Vienna": "W. Europe Standard Time",
	"Europe/Stockholm": "W. Europe Standard Time", "Europe/Oslo": "W. Europe Standard Time", "Europe/Zurich": "W. Europe Standard Time",
	"Europe/Luxembourg": "W. Europe Standard Time", "Europe/Monaco": "W. Europe Standard Time", "Europe/Malta": "W. Europe Standard Time",
	"Europe/Brussels": "Romance Standard Time", "Europe/Madrid": "Romance Standard Time", "Europe/Copenhagen": "Romance Standard Time",
	"Europe/Prague": "Central Europe Standard Time", "Europe/Belgrade": "Central Europe Standard Time", "Europe/Bratislava": "Central Europe Standard Time",
	"Europe/Ljubljana": "Central Europe Standard Time", "Europe/Zagreb": "Central European Standard Time", "Europe/Sarajevo": "Central European Standard Time",
	"Europe/Lisbon": "GMT Standard Time", "Europe/Dublin": "GMT Standard Time", "Atlantic/Canary": "GMT Standard Time",
	"Europe/Helsinki": "FLE Standard Time", "Europe/Riga": "FLE Standard Time", "Europe/Tallinn": "FLE Standard Time", "Europe/Vilnius": "FLE Standard Time",
	"Europe/Kyiv": "FLE Standard Time", "Europe/Sofia": "FLE Standard Time", "Europe/Athens": "GTB Standard Time",
	"America/Toronto": "Eastern Standard Time", "America/Detroit": "Eastern Standard Time", "America/Vancouver": "Pacific Standard Time",
	"America/Edmonton": "Mountain Standard Time", "America/Winnipeg": "Central Standard Time", "America/Indiana/Indianapolis": "US Eastern Standard Time",
	"Asia/Hong_Kong": "China Standard Time", "Asia/Calcutta": "India Standard Time", "Asia/Yangon": "Myanmar Standard Time",
	"Asia/Ho_Chi_Minh": "SE Asia Standard Time", "Asia/Jakarta": "SE Asia Standard Time", "Australia/Melbourne": "AUS Eastern Standard Time",
	"Australia/Canberra": "AUS Eastern Standard Time", "America/Argentina/Buenos_Aires": "Argentina Standard Time", "America/Lima": "SA Pacific Standard Time",
	"Africa/Accra": "Greenwich Standard Time", "Atlantic/Reykjavik": "Greenwich Standard Time", "Europe/Kiev": "FLE Standard Time",
}

// WindowsZone returns the Windows id for an IANA zone ("" if unknown).
func WindowsZone(iana string) string {
	if iana == "" || iana == "UTC" || iana == "Etc/UTC" {
		return "UTC"
	}
	if w, ok := extraWindows[iana]; ok {
		return w
	}
	for w, z := range windowsZones {
		if z == iana && !strings.HasPrefix(w, "UTC") && w != "Coordinated Universal Time" {
			return w
		}
	}
	return ""
}
