package market

import (
	"log"

	"github.com/quickfixgo/quickfix"
	"github.com/quickfixgo/quickfix/enum"
	"github.com/quickfixgo/quickfix/field"
	"github.com/quickfixgo/quickfix/fix44/marketdatarequest"
	"github.com/quickfixgo/quickfix/fix44/marketdatasnapshotfullrefresh"
	"pricing-service/engine"
)

type FIXApplication struct {
	calculator *engine.Calculator
	SessionID  quickfix.SessionID
}

func NewFIXApplication(calc *engine.Calculator) *FIXApplication {
	return &FIXApplication{
		calculator: calc,
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
	// In production, you might fetch this list from the database.
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
		// Indices could be added if needed
	}

	request := marketdatarequest.New(
		field.NewMDReqID("REQ1"),
		field.NewSubscriptionRequestType(enum.SubscriptionRequestType_SNAPSHOT_PLUS_UPDATES),
		field.NewMarketDepth(0), // top of book only if 1, full if 0. usually 1 is best for top.
	)
	
	// QuickFix uses 0 for full book, 1 for top of book. 
	// The python script didn't specify exactly, assuming top of book is fine. We will request full depth just in case.

	request.Set(field.NewMDUpdateType(enum.MDUpdateType_FULL_REFRESH))

	// Add entry types (Bid and Ask)
	entryTypes := quickfix.NewRepeatingGroup(
		field.NewNoMDEntryTypes(2),
		field.NewMDEntryType(enum.MDEntryType_BID),
		field.NewMDEntryType(enum.MDEntryType_BID), // field ordering
	)
    // quickfixgo way to do this:
    groupType := marketdatarequest.NewNoMDEntryTypesRepeatingGroup()
    entryBid := groupType.Add()
    entryBid.SetMDEntryType(enum.MDEntryType_BID)
    entryAsk := groupType.Add()
    entryAsk.SetMDEntryType(enum.MDEntryType_OFFER)
	request.SetNoMDEntryTypes(groupType)

	// Add symbols
	symbolsGroup := marketdatarequest.NewNoRelatedSymRepeatingGroup()
	for _, sym := range symbols {
		s := symbolsGroup.Add()
		s.SetSymbol(sym)
	}
	request.SetNoRelatedSym(symbolsGroup)

	err := quickfix.SendToTarget(request, sessionID)
	if err != nil {
		log.Printf("Error sending Market Data Request: %v\n", err)
	}
}

// OnLogout implemented as part of quickfix.Application interface
func (f *FIXApplication) OnLogout(sessionID quickfix.SessionID) {
	log.Printf("FIX Session logged out: %s\n", sessionID.String())
}

// ToAdmin implemented as part of quickfix.Application interface
func (f *FIXApplication) ToAdmin(msg *quickfix.Message, sessionID quickfix.SessionID) {
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
func (f *FIXApplication) FromApp(msg *quickfix.Message, sessionID quickfix.SessionID) quickfix.MessageRejectError {
	msgType, err := msg.Header.GetString(field.MsgTypeTag)
	if err != nil {
		return nil
	}

	// We only care about W (MarketDataSnapshotFullRefresh)
	if msgType == string(enum.MsgType_MARKET_DATA_SNAPSHOT_FULL_REFRESH) {
		f.handleMarketData(msg)
	}

	return nil
}

func (f *FIXApplication) handleMarketData(msg *quickfix.Message) {
	refresh := marketdatasnapshotfullrefresh.FromMessage(msg)
	
	symbol, err := refresh.GetSymbol()
	if err != nil {
		return
	}

	entries, err := refresh.GetNoMDEntries()
	if err != nil {
		return
	}

	var bid, ask float64

	for i := 0; i < entries.Len(); i++ {
		entry := entries.Get(i)
		
		entryType, err := entry.GetMDEntryType()
		if err != nil {
			continue
		}

		price, err := entry.GetMDEntryPx()
		if err != nil {
			continue
		}
        priceF, _ := price.Float64()

		if entryType == enum.MDEntryType_BID {
			bid = priceF
		} else if entryType == enum.MDEntryType_OFFER {
			ask = priceF
		}
	}

	if bid > 0 && ask > 0 {
		// Drop tick into isolated memory channel immediately (nanoseconds)
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
