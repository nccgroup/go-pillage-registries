package pillage

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"sync"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/v1/cache"
)

//ImageData represents an image enumerated from a registry or alternatively an error that occured while enumerating a registry.
type ImageData struct {
	Reference  string
	Registry   string
	Repository string
	Tag        string
	Manifest   string
	Config     string
	Error      error
}

//StorageOptions is passed to ImageData.Store to set the location and options for pulling the image data.
type StorageOptions struct {
	CachePath    string
	ResultsPath  string
	StoreImages  bool
	CraneOptions []crane.Option
}

//MakeCraneOption initalizes an array of crane options for use when interacting with a registry
func MakeCraneOptions(insecure bool) (options []crane.Option) {
	if insecure {
		options = append(options, crane.Insecure)
	}

	return options
}

func securejoin(paths ...string) (out string) {
	for _, path := range paths {
		out = filepath.Join(out, filepath.Clean("/"+path))
	}
	return out
}

//Store will output the information enumerated from an image to an output directory and optionally will pull the image filesystems as well
func (image *ImageData) Store(options *StorageOptions) error {
	log.Printf("Storing results for image: %s", image.Reference)

	//make image output dir
	imagePath := filepath.Join(options.ResultsPath, securejoin(image.Registry, image.Repository, image.Tag))
	err := os.MkdirAll(imagePath, os.ModePerm)
	if err != nil {
		log.Printf("Error making storage path %s: %v", imagePath, err)
		return err
	}

	log.Printf("Storing results for image: %s", image.Reference)

	//store image config
	if image.Config != "" {
		configPath := path.Join(imagePath, "config.json")
		//configFile, err := os.Create(configPath, os.ModePerm)
		//defer configFile.Close()
		err := ioutil.WriteFile(configPath, []byte(image.Config), os.ModePerm)
		if err != nil {
			log.Printf("Error making config file %s: %v", configPath, err)
		}
	}

	//store image manifest
	if image.Manifest != "" {
		manifestPath := path.Join(imagePath, "manifest.json")
		err := ioutil.WriteFile(manifestPath, []byte(image.Manifest), os.ModePerm)
		if err != nil {
			log.Printf("Error making manifest file %s: %v", manifestPath, err)
		}
	}

	//pull and save the image if asked
	if image.Error == nil && options.StoreImages {

		fs, err := crane.Pull(image.Reference, options.CraneOptions...)
		if err != nil {
			image.Error = errors.New(image.Error.Error() + err.Error())
		}
		if options.CachePath != "" {
			fs = cache.Image(fs, cache.NewFilesystemCache(options.CachePath))
		}

		fsPath := path.Join(imagePath, "filesystem.tar")
		if err := crane.Save(fs, image.Reference, fsPath); err != nil {
			log.Printf("Error saving tarball %s: %v", fsPath, err)
			if image.Error == nil {
				image.Error = err
			} else {
				image.Error = errors.New(image.Error.Error() + err.Error())
			}
		}
	}

	//store errors
	if image.Error != nil {
		errorPath := path.Join(imagePath, "errors.log")
		err := ioutil.WriteFile(errorPath, []byte(image.Error.Error()), os.ModePerm)
		if err != nil {
			log.Printf("Error making error file %s: %v", errorPath, err)
		}
	}
	return image.Error
}

//EnumImage will read a specific image from a remote registry and returns the result asynchronously.
func EnumImage(reg string, repo string, tag string, options ...crane.Option) <-chan *ImageData {
	out := make(chan *ImageData)

	ref := fmt.Sprintf("%s/%s:%s", reg, repo, tag)

	go func(ref string) {
		defer close(out)

		result := &ImageData{
			Reference:  ref,
			Registry:   reg,
			Repository: repo,
			Tag:        tag,
		}

		manifest, err := crane.Manifest(ref, options...)
		if err != nil {
			log.Printf("Error fetching manifest for image %s: %s", ref, err)
			result.Error = err
		}
		result.Manifest = string(manifest)

		config, err := crane.Config(ref, options...)
		if err != nil {
			log.Printf("Error fetching config for image %s: %s (the config may be in the manifest itself)", ref, err)
		}
		result.Config = string(config)

		out <- result
	}(ref)

	return out
}

//EnumRepository will read all images tagged in a specific repository on a remote registry and returns the results asynchronously.
//If a list of tags is not supplied, a list will be enumerated from the registry's API.
func EnumRepository(reg string, repo string, tags []string, options ...crane.Option) <-chan *ImageData {
	out := make(chan *ImageData)
	ref := fmt.Sprintf("%s/%s", reg, repo)
	log.Printf("Repo: %s", ref)

	go func(ref string) {
		defer close(out)

		if len(tags) == 0 {
			var err error
			tags, err = crane.ListTags(ref, options...)

			if err != nil {
				log.Printf("Error listing tags for %s: %s", ref, err)
				out <- &ImageData{
					Reference:  ref,
					Registry:   reg,
					Repository: repo,
					Error:      err,
				}
			}
		}

		var wg sync.WaitGroup

		for _, tag := range tags {
			wg.Add(1)
			go func(tag string) {
				defer wg.Done()
				images := EnumImage(reg, repo, tag, options...)
				for image := range images {
					out <- image
				}
			}(tag)
		}

		wg.Wait()
		return
	}(ref)
	return out
}

//EnumRegistry will read all images cataloged on a remote registry and returns the results asynchronously.
//If lists of repositories and tags are not supplied, lists will be enumerated from the registry's API.
func EnumRegistry(reg string, repos []string, tags []string, options ...crane.Option) <-chan *ImageData {
	out := make(chan *ImageData)
	log.Printf("Registry: %s\n", reg)

	go func() {
		defer close(out)

		if len(repos) == 0 {
			var err error
			repos, err = crane.Catalog(reg, options...)

			if err != nil {
				log.Printf("Error listing repos for %s: (%T) %s", reg, err, err)
				out <- &ImageData{
					Reference: reg,
					Registry:  reg,
					Error:     err,
				}
			}
		}

		var wg sync.WaitGroup

		for _, repo := range repos {
			wg.Add(1)
			go func(repo string) {
				defer wg.Done()
				images := EnumRepository(reg, repo, tags, options...)
				for image := range images {
					out <- image
				}
			}(repo)

		}
		wg.Wait()
	}()
	return out
}

//EnumRegistries will read all images cataloged by a set of remote registries and returns the results asynchronously.
//If lists of repositories and tags are not supplied, lists will be enumerated from the registry's API.
func EnumRegistries(regs []string, repos []string, tags []string, options ...crane.Option) <-chan *ImageData {
	out := make(chan *ImageData)
	go func() {
		defer close(out)

		if len(regs) == 0 {
			err := errors.New("No Registries supplied")
			log.Println(err)
			out <- &ImageData{
				Reference: "",
				Error:     err,
			}
			return
		}

		var wg sync.WaitGroup

		for _, reg := range regs {
			wg.Add(1)
			go func(reg string) {
				defer wg.Done()
				images := EnumRegistry(reg, repos, tags, options...)
				for image := range images {
					out <- image
				}
			}(reg)

		}
		wg.Wait()
	}()
	return out
}
