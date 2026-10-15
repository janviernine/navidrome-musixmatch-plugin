package musixmatch

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Myzel394/navidrome-musixmatch-plugin/plugin/utils"
	"github.com/navidrome/navidrome/plugins/pdk/go/host"
	"github.com/navidrome/navidrome/plugins/pdk/go/lyrics"
	"github.com/navidrome/navidrome/plugins/pdk/go/pdk"
	"github.com/stretchr/testify/mock"
)

// Musixmatch keeps duplicate track entries per song: one twin carries synced
// lyrics, the other carries nothing. The direct matcher call resolves to one
// twin and stops; the sweep must find the synced sibling.
func TestFetchLyricsStoryTimeDuplicateSweep(t *testing.T) {
	mockStoryTimeDuplicateAPI(t)

	resp, format, err, failure, success, _ := FetchLyrics(lyrics.GetLyricsRequest{
		Track: lyrics.TrackInfo{
			Artist:   "Fivio Foreign",
			Title:    "Story Time",
			Duration: 162,
		},
	})
	if err != nil {
		t.Fatalf("FetchLyrics returned error: %v", err)
	}
	if failure != nil {
		t.Fatalf("FetchLyrics returned failure: %v", failure)
	}
	if success == nil {
		t.Fatal("expected success metadata")
	}
	if format != utils.LyricsFormatSyncedLRC {
		t.Fatalf("expected synced LRC format, got %v", format)
	}
	if len(resp.Lyrics) == 0 {
		t.Fatal("expected at least one lyrics result")
	}
	if !strings.Contains(resp.Lyrics[0].Text, "[00:") {
		t.Fatalf("expected synced LRC lyrics, got %q", resp.Lyrics[0].Text)
	}
	if !strings.Contains(strings.ToLower(resp.Lyrics[0].Text), "sad story") {
		t.Fatalf("expected sweep to resolve the synced twin, got %q", resp.Lyrics[0].Text)
	}

	host.CacheMock.AssertExpectations(t)
	host.HTTPMock.AssertExpectations(t)
	pdk.PDKMock.AssertExpectations(t)
}

func mockStoryTimeToken(t *testing.T) {
	t.Helper()
	originalReader := mobileExperimentRandomReader
	mobileExperimentRandomReader = bytes.NewReader([]byte{1, 'f', 'i', 'x', 't', 'u', 'r', 'e', '-', 'g', 'u', 'i', 'd', '-', '0', '0', '1'})

	tokenBody := []byte(`{"message":{"header":{"status_code":200},"body":{"user_token":"fixture-token"}}}`)

	host.CacheMock.On("GetString", mobileTokenCache).Return("", false, nil).Once()
	host.CacheMock.On("SetString", mobileTokenCache, mock.AnythingOfType("string"), int64(mobileTokenTTL/time.Second)).Return(nil).Once()
	pdk.PDKMock.On("Log", mock.Anything, mock.Anything).Return()
	pdk.PDKMock.On("GetConfig", mock.Anything).Return("", false).Maybe()
	host.HTTPMock.On("Send", mock.MatchedBy(func(request host.HTTPRequest) bool {
		return request.Method == pdk.MethodGet.String() &&
			request.NoFollowRedirects &&
			strings.Contains(request.URL, "apic-appmobile.musixmatch.com") &&
			strings.Contains(request.URL, "token.get")
	})).Return(&host.HTTPResponse{StatusCode: int32(utils.HTTPStatusOK), Body: tokenBody}, nil).Once()

	t.Cleanup(func() {
		mobileExperimentRandomReader = originalReader
		host.CacheMock.ExpectedCalls = nil
		host.CacheMock.Calls = nil
		host.HTTPMock.ExpectedCalls = nil
		host.HTTPMock.Calls = nil
		pdk.PDKMock.ExpectedCalls = nil
		pdk.PDKMock.Calls = nil
	})
}

func mockStoryTimeDuplicateAPI(t *testing.T) {
	t.Helper()
	mockStoryTimeToken(t)

	// Direct matcher macro resolves to the empty twin: identity matches but
	// every lyrics call is empty.
	emptyMacro := macroResponseJSON(t, "", "")
	host.HTTPMock.On("Send", mock.MatchedBy(func(request host.HTTPRequest) bool {
		return strings.Contains(request.URL, "macro.subtitles.get") &&
			strings.Contains(request.URL, "q_track=Story+Time") &&
			!strings.Contains(request.URL, "track_id=")
	})).Return(&host.HTTPResponse{StatusCode: int32(utils.HTTPStatusOK), Body: emptyMacro}, nil).Once()

	// Candidate search returns the empty twin first, synced twin second.
	searchBody, _ := json.Marshal(map[string]any{"message": map[string]any{
		"header": map[string]any{"status_code": 200},
		"body": map[string]any{"track_list": []any{
			map[string]any{"track": map[string]any{"track_id": 224027345, "track_name": "Story Time", "artist_name": "Fivio Foreign", "album_name": "Story Time", "has_lyrics": 1, "instrumental": 0}},
			map[string]any{"track": map[string]any{"track_id": 223175197, "track_name": "Story Time", "artist_name": "Fivio Foreign", "album_name": "Story Time", "has_lyrics": 1, "instrumental": 0}},
		}},
	}})
	host.HTTPMock.On("Send", mock.MatchedBy(func(request host.HTTPRequest) bool {
		return strings.Contains(request.URL, "track.search") &&
			strings.Contains(request.URL, "q_track=Story+Time")
	})).Return(&host.HTTPResponse{StatusCode: int32(utils.HTTPStatusOK), Body: searchBody}, nil).Once()

	// Explicit track_id macros: empty twin stays empty, synced twin delivers.
	host.HTTPMock.On("Send", mock.MatchedBy(func(request host.HTTPRequest) bool {
		return strings.Contains(request.URL, "macro.subtitles.get") &&
			strings.Contains(request.URL, "track_id=224027345")
	})).Return(&host.HTTPResponse{StatusCode: int32(utils.HTTPStatusOK), Body: emptyMacro}, nil).Once()
	syncedMacro := macroResponseJSON(t, "[00:12.85] Let me tell a little sad story about this young boy\n", "")
	host.HTTPMock.On("Send", mock.MatchedBy(func(request host.HTTPRequest) bool {
		return strings.Contains(request.URL, "macro.subtitles.get") &&
			strings.Contains(request.URL, "track_id=223175197")
	})).Return(&host.HTTPResponse{StatusCode: int32(utils.HTTPStatusOK), Body: syncedMacro}, nil).Once()
}

// Macro envelope with a track identity plus optional subtitle/plain bodies.
// Empty strings produce empty-but-present calls, like the real API.
func macroResponseJSON(t *testing.T, subtitleText, plainText string) []byte {
	t.Helper()
	trackBody, _ := json.Marshal(map[string]any{"track": map[string]string{"track_name": "Story Time", "artist_name": "Fivio Foreign", "album_name": "Story Time"}})
	macro := macroBody{MacroCalls: map[string]macroResponse{}}
	track := macroResponse{}
	track.Message.Header.StatusCode = utils.HTTPStatusOK
	track.Message.Body = trackBody
	macro.MacroCalls["matcher.track.get"] = track
	subtitle := macroResponse{}
	subtitle.Message.Header.StatusCode = utils.HTTPStatusOK
	subBody, _ := json.Marshal(map[string]any{"subtitle_list": []any{map[string]any{"subtitle": map[string]string{"subtitle_body": subtitleText, "subtitle_language": "en"}}}})
	subtitle.Message.Body = subBody
	macro.MacroCalls["track.subtitles.get"] = subtitle
	plain := macroResponse{}
	plain.Message.Header.StatusCode = utils.HTTPStatusOK
	plainBody, _ := json.Marshal(map[string]any{"lyrics": map[string]string{"lyrics_body": plainText, "lyrics_language": "en"}})
	plain.Message.Body = plainBody
	macro.MacroCalls["track.lyrics.get"] = plain
	body, _ := json.Marshal(macro)
	outer := macroResponse{}
	outer.Message.Header.StatusCode = utils.HTTPStatusOK
	outer.Message.Body = body
	out, _ := json.Marshal(outer)
	return out
}
