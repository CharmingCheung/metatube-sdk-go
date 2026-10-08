package avbase

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/duga"
	"github.com/metatube-community/metatube-sdk-go/provider/fanza"
	"github.com/metatube-community/metatube-sdk-go/provider/getchu"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
	"github.com/metatube-community/metatube-sdk-go/provider/mgstage"
	"github.com/metatube-community/metatube-sdk-go/provider/pcolle"
)

var (
	_ provider.MovieProvider = (*AVBase)(nil)
	_ provider.MovieSearcher = (*AVBase)(nil)
	_ provider.Fetcher       = (*AVBase)(nil)
	_ provider.ConfigSetter  = (*AVBase)(nil)
)

const (
	Name     = "AVBASE"
	Priority = 1000 - 4
)

const (
	baseURL  = "https://www.avbase.net/"
	movieURL = "https://www.avbase.net/works/%s"
)

type AVBase struct {
	*fetch.Fetcher
	*scraper.Scraper
	pages            *pageClient
	sourceEnrichment bool
	providers        map[string]provider.MovieProvider
}

func New() *AVBase {
	return &AVBase{
		Fetcher: fetch.Default(&fetch.Config{SkipVerify: true}),
		Scraper: scraper.NewDefaultScraper(
			Name, baseURL, Priority, language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Referer": baseURL,
			})),
		pages: newPageClient(),
		providers: map[string]provider.MovieProvider{
			"duga":    duga.New(),
			"fanza":   fanza.New(),
			"getchu":  getchu.New(),
			"mgstage": mgstage.New(),
			"pcolle":  pcolle.New(),
		},
	}
}

func (ab *AVBase) NormalizeMovieID(id string) string {
	if !strings.Contains(id, ":") {
		return strings.ToUpper(id)
	}
	ss := strings.SplitN(id, ":", 2)
	prefix, workID := ss[0], ss[1]
	return ab.JoinPrefixID(prefix, workID)
}

func (ab *AVBase) GetMovieInfoByID(id string) (info *model.MovieInfo, err error) {
	return ab.GetMovieInfoByURL(fmt.Sprintf(movieURL, id))
}

func (ab *AVBase) ParseMovieIDFromURL(rawURL string) (string, error) {
	homepage, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return ab.NormalizeMovieID(path.Base(homepage.Path)), nil
}

func (ab *AVBase) GetMovieInfoByURL(rawURL string) (info *model.MovieInfo, err error) {
	id, err := ab.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return
	}

	var data struct {
		Work *workResponse `json:"work"`
	}
	if err = ab.pages.page("works/"+url.PathEscape(id), &data); err != nil {
		return nil, err
	}
	if data.Work == nil || data.Work.WorkID == "" {
		return nil, provider.ErrInfoNotFound
	}
	workInfo, err := ab.getMovieInfoFromWork(*data.Work)
	if err != nil {
		return nil, err
	}
	info = workInfo
	if ab.sourceEnrichment {
		if srcInfo, srcErr := ab.getMovieInfoFromSource(*data.Work); srcErr == nil {
			info = srcInfo
			if info.Maker == "" {
				info.Maker = workInfo.Maker
			}
			if info.Label == "" {
				info.Label = workInfo.Label
			}
			if info.Series == "" {
				info.Series = workInfo.Series
			}
			if info.Summary == "" {
				info.Summary = workInfo.Summary
			}
			if len(info.Genres) == 0 {
				info.Genres = workInfo.Genres
			}
			if len(workInfo.Actors) > 0 {
				info.Actors = workInfo.Actors
			}
			info.Number = workInfo.Number
		}
	}
	info.ID = workInfo.ID
	info.Provider = ab.Name()
	info.Homepage = fmt.Sprintf(movieURL, url.PathEscape(info.ID))
	if !info.IsValid() {
		return nil, provider.ErrInfoNotFound
	}
	return info, nil
}

func (ab *AVBase) getMovieInfoFromWork(work workResponse) (info *model.MovieInfo, err error) {
	info = &model.MovieInfo{
		ID:            ab.JoinPrefixID(work.Prefix, work.WorkID),
		Title:         work.Title,
		ReleaseDate:   parser.ParseDate(work.MinDate),
		Number:        work.WorkID,
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}
	sort.SliceStable(work.Products, func(i, j int) bool {
		// we want mgs > fanza > duga, etc.
		return work.Products[i].Source > work.Products[j].Source
	})
	for _, product := range work.Products {
		if info.Title == "" {
			info.Title = product.Title
		}
		if info.CoverURL == "" {
			info.CoverURL = product.ImageURL
		}
		if info.ThumbURL == "" {
			info.ThumbURL = product.ThumbnailURL
		}
		if info.Maker == "" {
			info.Maker = product.Maker.Name
		}
		if info.Label == "" {
			info.Label = product.Label.Name
		}
		if info.Series == "" {
			info.Series = product.Series.Name
		}
		if info.Summary == "" {
			info.Summary = product.ItemInfo.Description
		}
		if info.Director == "" {
			info.Director = product.ItemInfo.Director
		}
		if info.Runtime == 0 {
			info.Runtime, _ = strconv.Atoi(product.ItemInfo.Volume)
		}
		if time.Time(info.ReleaseDate).IsZero() {
			info.ReleaseDate = parser.ParseDate(product.Date)
		}
		if len(info.PreviewImages) == 0 {
			for _, sample := range product.SampleImageURLS {
				if sample.L == "" {
					continue
				}
				info.PreviewImages = append(info.PreviewImages, sample.L)
			}
		}
	}
	for _, genre := range work.Genres {
		info.Genres = append(info.Genres, genre.Name)
	}
	for _, cast := range work.Casts {
		info.Actors = append(info.Actors, cast.Actor.Name)
	}
	if len(info.Actors) == 0 {
		for _, actor := range work.Actors {
			info.Actors = append(info.Actors, actor.Name)
		}
	}
	return
}

func (ab *AVBase) getMovieInfoFromSource(work workResponse) (info *model.MovieInfo, err error) {
	for _, product := range work.Products {
		movieProvider, ok := ab.providers[product.Source]
		if !ok {
			continue
		}
		info, err = movieProvider.GetMovieInfoByID(product.ProductID)
		if err != nil || info == nil || !info.IsValid() {
			continue
		}
		break
	}
	if info == nil || !info.IsValid() {
		if err == nil {
			err = provider.ErrInfoNotFound
		}
	}
	return
}

func (ab *AVBase) NormalizeMovieKeyword(keyword string) string {
	if number.IsUncensored(keyword) || number.IsFC2(keyword) {
		return "" // no uncensored support.
	}
	return strings.ToUpper(keyword)
}

func (ab *AVBase) SearchMovie(keyword string) (results []*model.MovieSearchResult, err error) {
	var data struct {
		Works *[]workResponse `json:"works"`
	}
	if err = ab.pages.page("works?q="+url.QueryEscape(keyword), &data); err != nil {
		return nil, err
	}
	if data.Works == nil {
		return nil, fmt.Errorf("AVBASE: search response missing works")
	}
	for _, work := range *data.Works {
		info, parseErr := ab.getMovieInfoFromWork(work)
		if parseErr != nil {
			return nil, parseErr
		}
		info.Provider = ab.Name()
		info.Homepage = fmt.Sprintf(movieURL, url.PathEscape(info.ID))
		if info.IsValid() {
			results = append(results, info.ToSearchResult())
		}
	}
	return results, nil
}

func (ab *AVBase) JoinPrefixID(prefix, workID string) string {
	if strings.TrimSpace(prefix) == "" {
		return workID
	}
	return fmt.Sprintf("%s:%s", prefix, workID)
}

// GetBuildID is retained for SDK callers. Scraping no longer depends on it.
func (ab *AVBase) GetBuildID() (string, error) {
	data, err := ab.pages.document("")
	if err != nil {
		return "", err
	}
	if data.BuildID == "" {
		return "", fmt.Errorf("AVBASE: empty build id")
	}
	return data.BuildID, nil
}

func init() {
	provider.Register(Name, New)
}
