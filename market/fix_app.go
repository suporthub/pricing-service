package market

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/quickfixgo/quickfix"
	"pricing-service/engine"
)

// FIX Tag constants (raw integers, compatible with quickfixgo v0.9.x flat API)
const (
	tagMsgType                = quickfix.Tag(35)
	tagSymbol                 = quickfix.Tag(55)
	tagMDReqID                = quickfix.Tag(262)
	tagSubscriptionRequestType = quickfix.Tag(263)
	tagMarketDepth            = quickfix.Tag(264)
	tagMDUpdateType           = quickfix.Tag(265)
	tagNoMDEntryTypes         = quickfix.Tag(267)
	tagNoRelatedSym           = quickfix.Tag(146)
	tagMDEntryType            = quickfix.Tag(269)
	tagMDEntryPx              = quickfix.Tag(270)
	tagNoMDEntries            = quickfix.Tag(268)
	tagUsername                = quickfix.Tag(553)
	tagPassword               = quickfix.Tag(554)
	tagQuoteID                = quickfix.Tag(117)
	tagQuoteStatus            = quickfix.Tag(297)
)

type FIXApplication struct {
	calculator *engine.Calculator
	SessionID  quickfix.SessionID
	username   string
	password   string
}

func NewFIXApplication(calc *engine.Calculator, username string, password string) *FIXApplication {
	return &FIXApplication{
		calculator: calc,
		username:   username,
		password:   password,
	}
}

// OnCreate implemented as part of quickfix.Application interface
func (f *FIXApplication) OnCreate(sessionID quickfix.SessionID) {
	f.SessionID = sessionID
	log.Printf("FIX Session created: %s\n", sessionID.String())
}

// OnLogon implemented as part of quickfix.Application interface
func (f *FIXApplication) OnLogon(sessionID quickfix.SessionID) {
	log.Printf("FIX Session logged on: %s\n", sessionID.String())

	// Subscribe to predefined instruments
	symbols := []string{
		"AUDCAD",
            "AUDCHF","AUDJPY","AUDNZD","AUDSGD","AUDUSD",
            "BTCUSD","CADCHF","CADJPY","CHFJPY","CHFSGD",
            "EURAUD","EURCAD","EURCHF","EURCZK","EURGBP",
            "EURJPY","EURNOK","EURNZD","EURPLN","EURSEK",
            "EURSGD","EURTRY","EURUSD","EURZAR","GBPAUD",
            "GPBCAD","GPBCHF","GPBJPY","GBPNOK","GBPNZD",
            "GBPSEK","GBPUSD","NGAS","NOKSEK","NZDCAD",
            "NZDJPY","NZDUSD","SEKJPY","SGDJPY","US30",
            "USDCAD","USDCHF","USDCNH","USDCZK","USDJPY",
            "USDNOK","USDSEK","USDSGD","USDTHB","USDTRY",
            "USDZAR","XAGUSD","XAUUSD","ZARJPY","AUDCNH",
            "CNHJPY","EURCNH","GBPCNH","NZDCHF","NZDCNH",
            "AUDHKD","CADHKD","CHFHKD","EURDKK","EURHKD",
            "EURHUF","EURMXN","GBPHKD","GBPMXN","GBPSGD",
            "GBPTRY","HKDJPY","MXNJPY","NOKJPY","NZDHKD",
            "NZDSGD","SGDHKD","TRYJPY","USDDKK","USDHKD",
            "USDHUF","USDMXN","USDPLN","GAUCNH","GAUUSD",
            "XAGAUD","XAGEUR","XAUCNH","XAUEUR","XAUGBP",
            "XAUJPY","XAUAUD","XPDUSD","XPTUSD","UKOUSD",
            "USOUSD","COCOA","COFFEE_ARABICA","COFFEE_ROBUSTA",
            "COTTON","GASOIL","GASOLINE","ORANGE-JUICE",
            "SOYBEAN","SUGAR-RAW","WHEAT","GER40","NAS100",
            "SPX500","UK100","AUS200","ESP35","EU50",
            "FRA40","HK50","JPN225","US Small Cap 2000",
            "ADAUSD","ALGUSD","ATMUSD","AVAUSD","AXSUSD",
            "BATUSD","BNBUSD","BTCEUR","CHRUSD","COMUSD",
            "CRVUSD","DOGUSD","DOTUSD","DSHUSD","ENJUSD",
            "EOSUSD","GRTUSD","INCUSD","KNCUSD","LNKUSD",
            "LRCUSD","MANUSD","MKRUSD","NERUSD","OMGUSD",
            "SKLUSD","SNDUSD","SNXUSD","SOLUSD","SXPUSD",
            "TRXUSD","UNIUSD","XBNUSD","XETEUR","XETUSD",
            "XLCUSD","XLMUSD","XMRUSD","XRPUSD","XSIUSD",
            "XTZUSD","AVXUSD","XAU02","XAU04","XAU06",
            "XAU10","XAU12"
	}

	for i, sym := range symbols {
		msg := quickfix.NewMessage()
		msg.Header.SetField(tagMsgType, quickfix.FIXString("V")) // MarketDataRequest

		msg.Body.SetField(tagMDReqID, quickfix.FIXString(fmt.Sprintf("REQ%d", i)))
		msg.Body.SetField(tagSubscriptionRequestType, quickfix.FIXString("1")) // Snapshot + Updates
		msg.Body.SetField(tagMarketDepth, quickfix.FIXInt(1))                  // Top of book
		msg.Body.SetField(tagMDUpdateType, quickfix.FIXInt(0))                 // Full Refresh

		// NoMDEntryTypes group (Bid + Offer)
		entryTypesGroup := quickfix.NewRepeatingGroup(tagNoMDEntryTypes,
			quickfix.GroupTemplate{quickfix.GroupElement(tagMDEntryType)})
		entryTypesGroup.Add().SetField(tagMDEntryType, quickfix.FIXString("0")) // Bid
		entryTypesGroup.Add().SetField(tagMDEntryType, quickfix.FIXString("1")) // Offer
		msg.Body.SetGroup(entryTypesGroup)

		// NoRelatedSym group (the symbol)
		symbolGroup := quickfix.NewRepeatingGroup(tagNoRelatedSym,
			quickfix.GroupTemplate{quickfix.GroupElement(tagSymbol)})
		symbolGroup.Add().SetField(tagSymbol, quickfix.FIXString(sym))
		msg.Body.SetGroup(symbolGroup)

		err := quickfix.SendToTarget(msg, sessionID)
		if err != nil {
			log.Printf("Error sending Market Data Request for %s: %v\n", sym, err)
		}
	}
}

// OnLogout implemented as part of quickfix.Application interface
func (f *FIXApplication) OnLogout(sessionID quickfix.SessionID) {
	log.Printf("FIX Session logged out: %s\n", sessionID.String())
}

// ToAdmin injects credentials into Logon messages
func (f *FIXApplication) ToAdmin(msg *quickfix.Message, sessionID quickfix.SessionID) {
	msgType, err := msg.Header.GetString(tagMsgType)
	if err == nil && msgType == "A" { // MsgType_LOGON
		if f.username != "" {
			msg.Body.SetField(tagUsername, quickfix.FIXString(f.username))
		}
		if f.password != "" {
			msg.Body.SetField(tagPassword, quickfix.FIXString(f.password))
		}
	}
}

// ToApp implemented as part of quickfix.Application interface
func (f *FIXApplication) ToApp(msg *quickfix.Message, sessionID quickfix.SessionID) error {
	return nil
}

// FromAdmin implemented as part of quickfix.Application interface
func (f *FIXApplication) FromAdmin(msg *quickfix.Message, sessionID quickfix.SessionID) quickfix.MessageRejectError {
	return nil
}

// FromApp receives messages from the FIX server.
// Handles MarketDataSnapshotFullRefresh (W) and sends MassQuoteAck for tag 117
// to keep the LP price stream alive (matches Python LP connection logic).
func (f *FIXApplication) FromApp(msg *quickfix.Message, sessionID quickfix.SessionID) quickfix.MessageRejectError {
	msgType, err := msg.Header.GetString(tagMsgType)
	if err != nil {
		return nil
	}

	// We only care about W (MarketDataSnapshotFullRefresh)
	if msgType == "W" {
		f.handleMarketData(msg)
		// If QuoteID (tag 117) is present, respond with MassQuoteAcknowledgement
		// This is critical: PrimeXM expects an ACK and stops sending prices without it.
		f.sendMassQuoteAckIfPresent(msg, sessionID)
	}

	return nil
}

// sendMassQuoteAckIfPresent checks for QuoteID (tag 117) and responds with
// a MassQuoteAcknowledgement (35=b), matching the Python LP connection logic.
func (f *FIXApplication) sendMassQuoteAckIfPresent(msg *quickfix.Message, sessionID quickfix.SessionID) {
	quoteID, err := msg.Body.GetString(tagQuoteID)
	if err != nil {
		return // No QuoteID present, nothing to ack
	}

	ack := quickfix.NewMessage()
	ack.Header.SetField(tagMsgType, quickfix.FIXString("b")) // MassQuoteAcknowledgement
	ack.Body.SetField(tagQuoteID, quickfix.FIXString(quoteID))
	ack.Body.SetField(tagQuoteStatus, quickfix.FIXInt(0)) // Accepted

	if sendErr := quickfix.SendToTarget(ack, sessionID); sendErr != nil {
		log.Printf("Failed to send MassQuoteAck for QuoteID=%s: %v", quoteID, sendErr)
	}
}

func (f *FIXApplication) handleMarketData(msg *quickfix.Message) {
	// Parse the raw FIX message string directly — exactly like Python's fromApp.
	// This avoids quickfixgo's repeating group API which fails silently when the
	// GroupTemplate doesn't include all tags PrimeXM sends (e.g. 271 MDEntrySize).
	raw := msg.String()

	var sym string
	var bid, ask float64
	var currentEntryType string // "0" = bid, "1" = offer

	// Split by SOH (FIX delimiter 0x01)
	for _, field := range strings.Split(raw, "\x01") {
		if field == "" || !strings.Contains(field, "=") {
			continue
		}
		parts := strings.SplitN(field, "=", 2)
		tag := parts[0]
		val := parts[1]

		switch tag {
		case "55": // Symbol
			sym = val
		case "269": // MDEntryType (0=Bid, 1=Offer)
			currentEntryType = val
		case "270": // MDEntryPx
			if sym == "" {
				continue
			}
			price, err := strconv.ParseFloat(val, 64)
			if err != nil {
				continue
			}
			if currentEntryType == "0" {
				bid = price
			} else if currentEntryType == "1" {
				ask = price
			}
		}
	}

	if bid > 0 && ask > 0 && sym != "" {
		tick := engine.RawTick{
			Symbol: sym,
			Bid:    bid,
			Ask:    ask,
		}

		select {
		case f.calculator.TickPipe <- tick:
		default:
			// If buffer is full, drop tick. We only care about latest anyway.
		}
	}
}
