package market

import (
	"fmt"
	"log"
	"strconv"

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
		"AUDCAD", "AUDCHF", "AUDJPY", "AUDNZD", "AUDSGD", "AUDUSD",
		"BTCUSD", "CADCHF", "CADJPY", "CHFJPY", "CHFSGD", "EURAUD",
		"EURCAD", "EURCHF", "EURCZK", "EURGBP", "EURJPY", "EURNOK",
		"EURNZD", "EURPLN", "EURSEK", "EURSGD", "EURTRY", "EURUSD",
		"GBPAUD", "GBPCAD", "GBPCHF", "GBPJPY", "GBPNZD", "GBPSGD",
		"GBPUSD", "NZDCAD", "NZDCHF", "NZDJPY", "NZDUSD", "USDCAD",
		"USDCHF", "USDCNH", "USDCZK", "USDDKK", "USDHKD", "USDHUF",
		"USDJPY", "USDMXN", "USDNOK", "USDPLN", "USDSEK", "USDSGD",
		"USDTRY", "USDZAR", "XAGUSD", "XAUUSD", "UKOIL", "USOIL",
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
	symbol, err := msg.Body.GetString(tagSymbol)
	if err != nil {
		return
	}

	// Get the NoMDEntries repeating group
	group := quickfix.NewRepeatingGroup(tagNoMDEntries,
		quickfix.GroupTemplate{
			quickfix.GroupElement(tagMDEntryType),
			quickfix.GroupElement(tagMDEntryPx),
		})
	err2 := msg.Body.GetGroup(group)
	if err2 != nil {
		return
	}

	var bid, ask float64

	for i := 0; i < group.Len(); i++ {
		entry := group.Get(i)

		entryType, err := entry.GetString(tagMDEntryType)
		if err != nil {
			continue
		}

		priceStr, err := entry.GetString(tagMDEntryPx)
		if err != nil {
			continue
		}
		priceF, parseErr := strconv.ParseFloat(priceStr, 64)
		if parseErr != nil {
			continue
		}

		if entryType == "0" { // Bid
			bid = priceF
		} else if entryType == "1" { // Offer
			ask = priceF
		}
	}

	if bid > 0 && ask > 0 {
		tick := engine.RawTick{
			Symbol: symbol,
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
