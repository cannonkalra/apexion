package inference

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/apexion/apexion/internal/crawler/format"
	"github.com/apexion/apexion/internal/model"
)

// detector classifies a column's semantic meaning from its values.
type detector struct {
	semantic model.SemanticType
	pii      bool
	// match reports whether a single non-null value fits the detector.
	match func(v string) bool
	// nameHint boosts confidence when the column name contains one of these.
	nameHints []string
}

var (
	reEmail       = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	reURL         = regexp.MustCompile(`^(https?|ftp)://[^\s/$.?#].[^\s]*$`)
	rePhone       = regexp.MustCompile(`^\+?[0-9][0-9\-\s().]{6,18}[0-9]$`)
	reUUID        = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reIPv4        = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`)
	reIPv6        = regexp.MustCompile(`^([0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}$`)
	reSSN         = regexp.MustCompile(`^\d{3}-\d{2}-\d{4}$`)
	reCreditCard  = regexp.MustCompile(`^(?:\d[ -]?){13,19}$`)
	reZip         = regexp.MustCompile(`^\d{5}(-\d{4})?$`)
	reISOCurrency = regexp.MustCompile(`^[A-Z]{3}$`)
)

// dictionaries for closed-vocabulary detectors.
var (
	countryCodes  = set(strings.Fields("US CA GB DE FR IN CN JP BR AU MX ES IT NL SE NO FI DK PL RU ZA KR SG AE CH BE AT IE PT GR NZ"))
	countryNames  = set(lower(strings.Split("united states,canada,united kingdom,germany,france,india,china,japan,brazil,australia,mexico,spain,italy,netherlands,sweden,norway,finland,denmark,poland,russia,south africa,south korea,singapore,switzerland,belgium,austria,ireland,portugal,greece,new zealand", ",")))
	currencyCodes = set(strings.Fields("USD EUR GBP JPY CNY INR BRL AUD CAD MXN CHF SEK NOK DKK PLN RUB ZAR KRW SGD AED HKD NZD"))
	languageCodes = set(strings.Fields("en es fr de it pt ru zh ja ko ar hi bn nl sv no fi da pl tr"))
	genderValues  = set(lower([]string{"male", "female", "m", "f", "other", "non-binary", "nonbinary", "nb", "unknown"}))
)

// detectors are evaluated in priority order; the first to exceed the match
// threshold wins.
var detectors = []detector{
	{semantic: model.SemanticEmail, pii: true, match: reEmail.MatchString, nameHints: []string{"email", "e_mail", "mail"}},
	{semantic: model.SemanticUUID, pii: false, match: reUUID.MatchString, nameHints: []string{"uuid", "guid"}},
	{semantic: model.SemanticURL, pii: false, match: reURL.MatchString, nameHints: []string{"url", "link", "website", "uri"}},
	{semantic: model.SemanticSSN, pii: true, match: reSSN.MatchString, nameHints: []string{"ssn", "social"}},
	{semantic: model.SemanticIPAddress, pii: true, match: func(v string) bool { return reIPv4.MatchString(v) || reIPv6.MatchString(v) }, nameHints: []string{"ip", "ip_address", "ipaddr"}},
	{semantic: model.SemanticCreditCard, pii: true, match: matchCreditCard, nameHints: []string{"card", "credit", "cc_number", "ccnum"}},
	{semantic: model.SemanticZipCode, pii: false, match: reZip.MatchString, nameHints: []string{"zip", "postal", "postcode"}},
	{semantic: model.SemanticPhone, pii: true, match: matchPhone, nameHints: []string{"phone", "mobile", "tel", "cell", "fax"}},
	{semantic: model.SemanticLatitude, pii: false, match: matchLatitude, nameHints: []string{"lat", "latitude"}},
	{semantic: model.SemanticLongitude, pii: false, match: matchLongitude, nameHints: []string{"lon", "lng", "long", "longitude"}},
	{semantic: model.SemanticCurrency, pii: false, match: func(v string) bool {
		return currencyCodes[strings.ToUpper(v)] || reISOCurrency.MatchString(v) && currencyCodes[v]
	}, nameHints: []string{"currency", "ccy"}},
	{semantic: model.SemanticCountry, pii: false, match: matchCountry, nameHints: []string{"country", "nation"}},
	{semantic: model.SemanticLanguage, pii: false, match: func(v string) bool { return languageCodes[strings.ToLower(v)] }, nameHints: []string{"lang", "language", "locale"}},
	{semantic: model.SemanticGender, pii: true, match: func(v string) bool { return genderValues[strings.ToLower(strings.TrimSpace(v))] }, nameHints: []string{"gender", "sex"}},
}

// classifySemantic evaluates detectors over the sampled values and column name.
// It returns the winning semantic type, whether it is PII, and a confidence.
func classifySemantic(colName string, values []string) (model.SemanticType, bool, float64) {
	nonNull := values
	if len(nonNull) == 0 {
		return model.SemanticNone, false, 0
	}
	lowerName := strings.ToLower(colName)

	best := model.SemanticNone
	bestConf := 0.0
	bestPII := false

	for _, d := range detectors {
		matched := 0
		for _, v := range nonNull {
			if d.match(v) {
				matched++
			}
		}
		ratio := float64(matched) / float64(len(nonNull))
		nameBoost := 0.0
		for _, h := range d.nameHints {
			if strings.Contains(lowerName, h) {
				nameBoost = 0.2
				break
			}
		}
		conf := ratio + nameBoost
		// Require strong value agreement, or a name hint with decent agreement.
		if (ratio >= 0.9 || (nameBoost > 0 && ratio >= 0.5)) && conf > bestConf {
			best = d.semantic
			bestConf = conf
			bestPII = d.pii
		}
	}

	// Name-only heuristics for person names / addresses (hard to regex).
	if best == model.SemanticNone {
		switch {
		case containsAny(lowerName, "first_name", "last_name", "full_name", "fname", "lname", "customer_name", "person"):
			return model.SemanticName, true, 0.6
		case containsAny(lowerName, "address", "street", "addr_line", "city"):
			return model.SemanticAddress, true, 0.55
		}
	}

	if bestConf > 1 {
		bestConf = 1
	}
	return best, bestPII, bestConf
}

func matchPhone(v string) bool {
	// A date like 2023-01-15 superficially matches the phone pattern; reject it.
	if _, dt := format.DetectDateLayout(v); dt != model.TypeUnknown {
		return false
	}
	if !rePhone.MatchString(v) {
		return false
	}
	digits := 0
	for _, r := range v {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return digits >= 7 && digits <= 15
}

func matchCreditCard(v string) bool {
	if !reCreditCard.MatchString(v) {
		return false
	}
	return luhnValid(strings.NewReplacer(" ", "", "-", "").Replace(v))
}

func luhnValid(s string) bool {
	sum := 0
	alt := false
	for i := len(s) - 1; i >= 0; i-- {
		d := int(s[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0 && len(s) >= 13
}

func matchLatitude(v string) bool {
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && f >= -90 && f <= 90 && strings.Contains(v, ".")
}

func matchLongitude(v string) bool {
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && f >= -180 && f <= 180 && strings.Contains(v, ".")
}

func matchCountry(v string) bool {
	if countryCodes[strings.ToUpper(strings.TrimSpace(v))] && len(v) == 2 {
		return true
	}
	return countryNames[strings.ToLower(strings.TrimSpace(v))]
}

func set(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		if i != "" {
			m[i] = true
		}
	}
	return m
}

func lower(items []string) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = strings.ToLower(strings.TrimSpace(s))
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
