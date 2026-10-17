# vector-go-sdk

A go SDK for Vector! This is an ***early alpha*** and not all features are present.  

## Usage

Currently. the SDK expects to find in your home directory the Python SDK data.
It consists of a `.anki_vector` folder where a `sdk_config.ini` file is placed. This file contains the configuration for each robot:

```ini
[ROBOT SERIAL NO]
cert = path to certificate (not needed by the go SDK)
ip = robot ip
name = robot name
guid = token for connecting to the robot
```

When you instantiate the robot you provide the serial number, then the SDK code will parse the `.anki_vector/sdk_config.ini` and get the data it needs.
I develop and test this version of the go SDK using a production Vector 1.0 and [wirepod](https://github.com/kercre123/wire-pod).

### Wirepod Vectors
If you are working on wirepod, you do not need to create the sdk_config.ini file if you get the vector struct using a `NewWpExternal` function.
It will automatically search for the activated Vector with this serial number on wirepod API and connect to it.

## Examples

Please see the `cmd/examples` directory for usage examples.

## Known Issues

- FaceEnrollmentStart(personName string)
  - Adding a new face enrollment via SDK doesn't seem to work. The procedure completes but the face is not saved on robot
- SaveHiResCameraPicture(fileName string) 
  - This doesn't seem to work on my production Vector on Wirepod. Probably vector-cloud on the robot needs to be updated. But since it's a production robot I'm stuck to 1.8...
    Anyway the image is saved at the regular 360p size.

## Changelog 

+***RELEASE_ALPHA_22***
- Added a custom setting for game in progress. This way a game can progress towards multiple STT invocations and its 
  state can be made persistent

+***RELEASE_ALPHA_21***
- Added sdk-wrapper-oskr for OSKR-only functions:
  - TriggerWakeWord() to trigger a voice input prompt on Vector
- Added oskr.proto / oskr.pb.go, oskr_grpc.pb.go to allow communication with a custom grpc server to be run on OSKR
  See an example in examples/navigator, this needs to be used in conjunction with the server counterpart that you can
  find here: https://github.com/fforchino/vector-go-sdk-oskr-extensions

+***RELEASE_ALPHA_20***
- Improved TTS support. Added custom voice server that provides custom voices from different providers 
  (currently only using FakeYou.com)

+***RELEASE_ALPHA_19***
- GetEyeColor() in case of custom eye colors returns always full white

+***RELEASE_ALPHA_18***
- Fix InitSDKForWirepod() function 

+***RELEASE_ALPHA_17***
- Added a couple of extra fields in custom settings to use with VIM chat

+***RELEASE_ALPHA_16***
- Added SetChatTarget() and GetChatTarget() to save username with whom this bot is chatting with

+***RELEASE_ALPHA_15***
- Added GetStaticCameraPicture()
- Exposed a few image functions
- Fixed eSpeak TTS: now the proper language voice is used 
- Bugfix: Using events with wirepod crashes the application

+***RELEASE_ALPHA_14***
- Bugfix: TextOnImg uses an hardcoded data path
- Bugfix: InitSDK doesn't correctly initialize event stream (event example broken!)
- Bugfix: temporary path not correctly used

+***RELEASE_ALPHA_13***
- Set TTS output always at the maximum volume 

+***RELEASE_ALPHA_12***
- Bugfix: Language not correctly set to robot's locale language when initializing the sdk

+***RELEASE_ALPHA_11***
- Bugfix: SetRobotName doesn't write permanent data

+***RELEASE_ALPHA_10***
- Bugfix: SetRobotName doesn't write permanent data

+***RELEASE_ALPHA_09***
Integration with VectorX (https://github.com/fforchino/vectorx)

+***RELEASE_ALPHA_08***
Integration with Wirepod

+***RELEASE_ALPHA_07***
CODE REFACTORING aimed at:
- Make all paths os-independent
- Make all temporary files bot-dependent, to allow usage in a scenario where multiple robots are connected to an escape pod
- Organize external files in 3 categories:
  - (TMP) Temporary files living in a TmpPath, they are relative to the bot invoking a certain function
  - (DATA) Data files, they are read only files that the SDK needs, so they are shared among all bots
  - (NVM) Persistent cloud storage available to the bot
     +- The introduction of NVM allows the implementation of CustomSettings, an extra data structure that every bot
  now owns and contains new settings, like robot_name

Forgive me for the very quick description, I will soon document all properly in a wiki. The important thing is
that the GO SDK the way it is now is ready for the integration with Wirepod. Hooray!
Remember to get the updated version:
go get -u github.com/fforchino/vector-go-sdk/pkg/sdk-wrapper

***RELEASE_ALPHA_06***
- Image functions
  - Add the option to use Vector's eye color to blend the images with (kind of near monochrome effect)
- Audio functions
  - Use master volume to play audio files. Set the master volume higher if you want to play audio louder
- Settings
  - Added utility to get Vector's eye colors as RGB, and audio volume in the range 0..100 
- Examples
  - Add full example of "Roll a die" (uses the features above)

***RELEASE_ALPHA_05***
- Image functions
  - Added animated gif processing
  - Fixed a bug in image resize
  - Added fade in/fade out (from/to black) image transition 

***RELEASE_ALPHA_04***

- Added voice functions (internationalized TTS using htgo or espeak TTS engines)
  - Some voices are not directly supported in espeak (just need to figure out what dependencies to install)

***RELEASE_ALPHA_03***

- Removed dependency from OpenCV, because goCV sucks and requires user to download a lot of stuff. Better use OpenCv in Python!
- Bugfix: WriteText() / DisplayImage() / DisplayImageWithTransition() Image and text displayed have garbled colors

***RELEASE_ALPHA_02***

- To try to support hires camera image snapshot I added OpenCV as a dependency. To install OpenCV on a Raspberry Pi or other Linux environment, run setup.sh
- Added camera functions
- Added face enrollment functions
- Added events with examples (Vector roars when touched)

***RELEASE_ALPHA_01***

I have taken the go SDK and did some changes.
- Integrated the functions of the great Wire's [vector-web-api-app](https://github.com/kercre123/vector-web-api-app) as sdk_wrapper functions
- Added many examples:
- The SDK supports features in these fields: 
  - Audio playback and volume settings
  - Custom eye colors
  - Animations
  - Settings
