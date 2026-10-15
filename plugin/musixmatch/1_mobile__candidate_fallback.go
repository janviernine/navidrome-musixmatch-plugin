package musixmatch

import (
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/Myzel394/navidrome-musixmatch-plugin/plugin/utils"
	"github.com/navidrome/navidrome/plugins/pdk/go/lyrics"
)

// Mobile `track.search` response envelope. Same message/header/body shape as
// every other mobile API call, with a track list payload.
type mobileSearchBody struct {
	TrackList []struct {
		Track struct {
			TrackID      int    `json:"track_id"`
			TrackName    string `json:"track_name"`
			ArtistName   string `json:"artist_name"`
			AlbumName    string `json:"album_name"`
			HasLyrics    int    `json:"has_lyrics"`
			Instrumental int    `json:"instrumental"`
		} `json:"track"`
	} `json:"track_list"`
}

// Musixmatch keeps duplicate track entries per song (plain vs synced, solo vs
// feat. credit). The direct `macro.subtitles.get` matcher call resolves to one
// of them and stops there, so a synced copy sitting on a sibling duplicate is
// never tried. This fallback sweeps every lyrics-bearing candidate with an
// explicit `track_id`: synced first across all candidates, plain second.
func fetchLyricsFromMobileCandidates(input lyrics.GetLyricsRequest, token string, assignment mobileGUIDAssignment) (lyrics.GetLyricsResponse, utils.LyricsFormat, *utils.LookupSuccess) {
	if input.Track.Title == "" {
		return lyrics.GetLyricsResponse{}, utils.LyricsFormatUnknown, nil
	}
	query := url.Values{}
	query.Set("format", "json")
	query.Set("q_track", input.Track.Title)
	query.Set("q_artist", input.Track.Artist)
	query.Set("page", "1")
	query.Set("page_size", "10")
	query.Set("f_has_lyrics", "1")
	query.Set("usertoken", token)
	response, err := mobileGet("track.search", query, assignment)
	if err != nil {
		utils.LogInfof("mobile API: candidate search request failed error=%v", err)
		return lyrics.GetLyricsResponse{}, utils.LyricsFormatUnknown, nil
	}
	if response.Message.Header.StatusCode != utils.HTTPStatusOK {
		utils.LogInfof("mobile API: candidate search returned status=%d", response.Message.Header.StatusCode)
		return lyrics.GetLyricsResponse{}, utils.LyricsFormatUnknown, nil
	}
	var body mobileSearchBody
	if err := json.Unmarshal(response.Message.Body, &body); err != nil {
		utils.LogInfof("mobile API: candidate search response could not be parsed body_bytes=%d", len(response.Message.Body))
		return lyrics.GetLyricsResponse{}, utils.LyricsFormatUnknown, nil
	}
	candidates := make([]trackMetadata, 0, 5)
	candidateIDs := make([]int, 0, 5)
	seen := map[int]bool{}
	for _, item := range body.TrackList {
		track := item.Track
		if track.TrackID == 0 || seen[track.TrackID] || track.HasLyrics != 1 || track.Instrumental == 1 {
			continue
		}
		seen[track.TrackID] = true
		meta := trackMetadata{Artist: track.ArtistName, Title: track.TrackName, Album: track.AlbumName}
		if err := validateMatchedIdentity(input, meta, "mobile API candidates"); err != nil {
			continue
		}
		candidates = append(candidates, meta)
		candidateIDs = append(candidateIDs, track.TrackID)
		if len(candidateIDs) >= 5 {
			break
		}
	}
	utils.LogInfof("mobile API: candidate sweep trying track_ids=%d", len(candidateIDs))
	macros := make([]macroBody, 0, len(candidateIDs))
	for _, trackID := range candidateIDs {
		macroQuery := url.Values{}
		macroQuery.Set("format", "json")
		macroQuery.Set("namespace", "lyrics_richsynched")
		macroQuery.Set("subtitle_format", "lrc")
		macroQuery.Set("track_id", strconv.Itoa(trackID))
		macroQuery.Set("usertoken", token)
		macroResponse, err := mobileGet("macro.subtitles.get", macroQuery, assignment)
		if err != nil {
			utils.LogInfof("mobile API: candidate macro request failed track_id=%d error=%v", trackID, err)
			continue
		}
		if macroResponse.Message.Header.StatusCode != utils.HTTPStatusOK {
			utils.LogInfof("mobile API: candidate macro returned status=%d track_id=%d", macroResponse.Message.Header.StatusCode, trackID)
			continue
		}
		var macro macroBody
		if err := json.Unmarshal(macroResponse.Message.Body, &macro); err != nil {
			utils.LogInfof("mobile API: candidate macro response could not be parsed track_id=%d body_bytes=%d", trackID, len(macroResponse.Message.Body))
			continue
		}
		utils.LogInfof("mobile API: candidate macro resolved track_id=%d", trackID)
		macros = append(macros, macro)
	}
	// Pass 1: synced anywhere.
	for _, macro := range macros {
		if resp, ok := lyricsFromRichsync(macro.MacroCalls["track.richsync.get"]); ok && lyricsResponseAllowed(resp) {
			return resp, utils.LyricsFormatSyncedLRC, utils.NewLookupSuccess("mobile_synced")
		}
		if resp, ok := lyricsFromSubtitle(macro.MacroCalls["track.subtitles.get"]); ok && lyricsResponseAllowed(resp) {
			return resp, utils.LyricsFormatSyncedLRC, utils.NewLookupSuccess("mobile_synced")
		}
	}
	// Pass 2: plain fallback.
	for _, macro := range macros {
		if resp, ok := lyricsFromPlain(macro.MacroCalls["track.lyrics.get"]); ok && lyricsResponseAllowed(resp) {
			return resp, utils.LyricsFormatPlain, utils.NewLookupSuccess("mobile_plain")
		}
	}
	return lyrics.GetLyricsResponse{}, utils.LyricsFormatUnknown, nil
}
