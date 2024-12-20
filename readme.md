# Zap2It XMLTV Converter
This script converts TV listings from [Zap2It](https://tvschedule.zap2it.com/) into the XMLTV format, which can be used with media applications like [Jellyfin](https://jellyfin.org/) and [Emby](https://emby.media/).

It is written in Go and can be run on Windows, macOS, and Linux without any dependencies.
It can be run as a daemon to automatically update the listings every X days.

## Features

- Fetch TV listings from Zap2It.
- Convert listings to XMLTV format.
- Customize output with configuration.
- Run as a daemon to automatically update listings.

## Installation
Download the release for your platform from the releases page.

or build from source:

```bash
git clone https://github.com/spf13/zap2itxmltv.git
go build -o zap2itxmltv
```

## Usage

```bash
./zap2itxmltv [options]
```
Either put it in a cron job or just use -d to run it as a daemon.

Example to update every 5 days:
```bash 
./zap2itxmltv -d -i 5 -o tvschedule/guide.xmltv
```

### Options
 * `-c, --configfile`
   Path to the configuration file. Default: ./zap2itconfig.ini

 * `-o, --outputfile`
   Path to the output XMLTV file. Default: xmlguide.xmltv

 * `-l, --language`
   Language code for the guide data. Default: en

*  `-f, --findid`
   Find headendID and lineupID for your region.

*  `-d, --daemon`
   Run the script as a daemon. Fetches listings every X days (default: 7).

*  `-i, --interval`
   Interval in days to fetch listings when running as a daemon. Default: 7

## Configuration

Create a zap2itconfig.ini file with the following content:

```
[creds]
username: USERNAME
password: PASSWORD
[prefs]
country: USA
zipCode: ZIPCODE
historicalGuideDays: 14
lang: en
[lineup]
headendId: lineupId
lineupId: USA-lineupId-DEFAULT
device: - 
```

### Obtaining Zap2It Configuration Information
To use this script, you'll need a Zap2It account, your headendID, lineupID and device. Here's how to get them:


1. Sign up for a free account at [Zap2It](https://tvschedule.zap2it.com/).
2. Add your username, password, and zipCode to the configuration file.
3. Run the script with the `-f` option to find your headendID and lineupID.


## License
This project is licensed under the Apache License 2.0. See the LICENSE file for details.