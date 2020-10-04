package helper

/*
 * Copyright 2020 Aldelo, LP
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/aldelo/common/rest"
	"net"
	"net/url"
	"reflect"
	"strings"
	"time"
)

// GetNetListener triggers the specified port to listen via tcp
func GetNetListener(port uint) (net.Listener, error) {
	if l, e := net.Listen("tcp", fmt.Sprintf(":%d", port)); e != nil {
		return nil, fmt.Errorf("Listen Tcp on Port %d Failed: %v", port, e)
	} else {
		return l, nil
	}
}

// GetLocalIP returns the first non loopback ip
func GetLocalIP() string {
	if addrs, err := net.InterfaceAddrs(); err != nil {
		return ""
	} else {
		for _, a := range addrs {
			if ip, ok := a.(*net.IPNet); ok && !ip.IP.IsLoopback() && !ip.IP.IsInterfaceLocalMulticast() && !ip.IP.IsLinkLocalMulticast() && !ip.IP.IsLinkLocalUnicast() && !ip.IP.IsMulticast() && !ip.IP.IsUnspecified() {
				if ip.IP.To4() != nil {
					return ip.IP.String()
				}
			}
		}

		return ""
	}
}

// DnsLookupIps returns list of IPs for the given host
// if host is private on aws route 53, then lookup ip will work only when within given aws vpc that host was registered with
func DnsLookupIps(host string) (ipList []net.IP) {
	if ips, err := net.LookupIP(host); err != nil {
		return []net.IP{}
	} else {
		for _, ip := range ips {
			ipList = append(ipList, ip)
		}
		return ipList
	}
}

// DnsLookupSrvs returns list of IP and port addresses based on host
// if host is private on aws route 53, then lookup ip will work only when within given aws vpc that host was registered with
func DnsLookupSrvs(host string) (ipList []string) {
	if _, addrs, err := net.LookupSRV("", "", host); err != nil {
		return []string{}
	} else {
		for _, v := range addrs {
			ipList = append(ipList, fmt.Sprintf("%s:%d", v.Target, v.Port))
		}

		return ipList
	}
}

// ParseHostFromURL will parse out the host name from url
func ParseHostFromURL(url string) string {
	parts := strings.Split(strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(url), "https://", ""), "http://", ""), "/")

	if len(parts) >= 0 {
		return parts[0]
	} else {
		return ""
	}
}

// VerifyGoogleReCAPTCHAv2 will verify recaptcha v2 response data against given secret and obtain a response from google server
func VerifyGoogleReCAPTCHAv2(response string, secret string) (success bool, challengeTs time.Time, hostName string, err error) {
	if LenTrim(response) == 0 {
		return false, time.Time{}, "", fmt.Errorf("ReCAPTCHA Response From CLient is Required")
	}

	if LenTrim(secret) == 0 {
		return false, time.Time{}, "", fmt.Errorf("ReCAPTCHA Secret Key is Required")
	}

	u := fmt.Sprintf("https://www.google.com/recaptcha/api/siteverify?secret=%s&response=%s", url.PathEscape(secret), url.PathEscape(response))

	if statusCode, responseBody, e := rest.POST(u, []*rest.HeaderKeyValue{}, ""); e != nil {
		return false, time.Time{}, "", fmt.Errorf("ReCAPTCHA Service Failed: %s", e)
	} else {
		if statusCode != 200 {
			return false, time.Time{}, "", fmt.Errorf("ReCAPTCHA Service Failed: Status Code %d", statusCode)
		} else {
			m := make(map[string]json.RawMessage)
			if err = json.Unmarshal([]byte(responseBody), &m); err != nil {
				return false, time.Time{}, "", fmt.Errorf("ReCAPTCHA Service Response Failed: (Parse Json Response Error) %s", err)
			} else {
				if m == nil {
					return false, time.Time{}, "", fmt.Errorf("ReCAPTCHA Service Response Failed: %s", "Json Response Map Nil")
				} else {
					// response json from google is valid
					if strings.ToLower(string(m["success"])) == "true" {
						success = true
					}

					challengeTs = ParseDateTime(string(m["challenge_ts"]))
					hostName = string(m["hostname"])

					if !success {
						errs := string(m["error-codes"])
						s := []string{}

						if err = json.Unmarshal([]byte(errs), &s); err != nil {
							err = fmt.Errorf("Parse ReCAPTCHA Verify Errors Failed: %s", err)
						} else {
							buf := ""

							for _, v := range s {
								if LenTrim(v) > 0 {
									if LenTrim(buf) > 0 {
										buf += ", "
									}

									buf += v
								}
							}

							err = fmt.Errorf("ReCAPTCHA Verify Errors: %s", buf)
						}
					}

					return success, challengeTs, hostName, err
				}
			}
		}
	}
}

// StructToQueryParams marshals a struct pointer's fields to query params string,
// output query param names are based on values given in tagName,
// to exclude certain struct fields from being marshaled, include excludeTagName with - as value in struct definition
func StructToQueryParams(inputStructPtr interface{}, tagName string, excludeTagName string) (string, error) {
	if inputStructPtr == nil {
		return "", fmt.Errorf("StructToQueryParams Require Input Struct Variable Pointer")
	}

	if LenTrim(tagName) == 0 {
		return "", fmt.Errorf("StructToQueryParams Require TagName (Tag Name defines query parameter name)")
	}

	s := reflect.ValueOf(inputStructPtr).Elem()

	if s.Kind() != reflect.Struct {
		return "", fmt.Errorf("StructToQueryParams Require Struct Object")
	}

	output := ""

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() {
			if tag := field.Tag.Get(tagName); LenTrim(tag) > 0 {
				if LenTrim(excludeTagName) > 0 {
					if field.Tag.Get(excludeTagName) == "-" {
						continue
					}
				}

				buf := ""

				switch o.Kind() {
				case reflect.String:
					buf = o.String()
				case reflect.Bool:
					if o.Bool() {
						buf = "true"
					} else {
						buf = "false"
					}
				case reflect.Int8:
					fallthrough
				case reflect.Int16:
					fallthrough
				case reflect.Int:
					fallthrough
				case reflect.Int32:
					fallthrough
				case reflect.Int64:
					buf = Int64ToString(o.Int())
				case reflect.Float32:
					fallthrough
				case reflect.Float64:
					buf = FloatToString(o.Float())
				case reflect.Uint8:
					fallthrough
				case reflect.Uint16:
					fallthrough
				case reflect.Uint:
					fallthrough
				case reflect.Uint32:
					fallthrough
				case reflect.Uint64:
					buf = UInt64ToString(o.Uint())
				default:
					switch f := o.Interface().(type) {
					case sql.NullString:
						buf = FromNullString(f)
					case sql.NullBool:
						if FromNullBool(f) {
							buf = "true"
						} else {
							buf = "false"
						}
					case sql.NullFloat64:
						buf = FloatToString(FromNullFloat64(f))
					case sql.NullInt32:
						buf = Itoa(FromNullInt(f))
					case sql.NullInt64:
						buf = Int64ToString(FromNullInt64(f))
					case sql.NullTime:
						buf = FromNullTime(f).String()
					case time.Time:
						buf = f.String()
					default:
						continue
					}
				}

				if LenTrim(output) > 0 {
					output += "&"
				}

				output += fmt.Sprintf("%s=%s", tag, url.PathEscape(buf))
			}
		}
	}

	if LenTrim(output) == 0 {
		return "", fmt.Errorf("StructToQueryParameters Yielded Blank Output")
	} else {
		return output, nil
	}
}

// StructToJson marshals a struct pointer's fields to json string,
// output json names are based on values given in tagName,
// to exclude certain struct fields from being marshaled, include excludeTagName with - as value in struct definition
func StructToJson(inputStructPtr interface{}, tagName string, excludeTagName string) (string, error) {
	if inputStructPtr == nil {
		return "", fmt.Errorf("StructToJson Require Input Struct Variable Pointer")
	}

	if LenTrim(tagName) == 0 {
		return "", fmt.Errorf("StructToJson Require TagName (Tag Name defines Json name)")
	}

	s := reflect.ValueOf(inputStructPtr).Elem()

	if s.Kind() != reflect.Struct {
		return "", fmt.Errorf("StructToJson Require Struct Object")
	}

	output := ""

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() {
			if tag := field.Tag.Get(tagName); LenTrim(tag) > 0 {
				if LenTrim(excludeTagName) > 0 {
					if field.Tag.Get(excludeTagName) == "-" {
						continue
					}
				}

				buf := ""

				switch o.Kind() {
				case reflect.String:
					buf = o.String()
				case reflect.Bool:
					if o.Bool() {
						buf = "true"
					} else {
						buf = "false"
					}
				case reflect.Int8:
					fallthrough
				case reflect.Int16:
					fallthrough
				case reflect.Int:
					fallthrough
				case reflect.Int32:
					fallthrough
				case reflect.Int64:
					buf = Int64ToString(o.Int())
				case reflect.Float32:
					fallthrough
				case reflect.Float64:
					buf = FloatToString(o.Float())
				case reflect.Uint8:
					fallthrough
				case reflect.Uint16:
					fallthrough
				case reflect.Uint:
					fallthrough
				case reflect.Uint32:
					fallthrough
				case reflect.Uint64:
					buf = UInt64ToString(o.Uint())
				default:
					switch f := o.Interface().(type) {
					case sql.NullString:
						buf = FromNullString(f)
					case sql.NullBool:
						if FromNullBool(f) {
							buf = "true"
						} else {
							buf = "false"
						}
					case sql.NullFloat64:
						buf = FloatToString(FromNullFloat64(f))
					case sql.NullInt32:
						buf = Itoa(FromNullInt(f))
					case sql.NullInt64:
						buf = Int64ToString(FromNullInt64(f))
					case sql.NullTime:
						buf = FromNullTime(f).String()
					case time.Time:
						buf = f.String()
					default:
						continue
					}
				}

				buf = strings.Replace(buf, `"`, `\"`, -1)
				buf = strings.Replace(buf, `'`, `\'`, -1)

				if LenTrim(output) > 0 {
					output += ", "
				}

				output += fmt.Sprintf(`"%s":"%s"`, tag, JsonToEscaped(buf))
			}
		}
	}

	if LenTrim(output) == 0 {
		return "", fmt.Errorf("StructToJson Yielded Blank Output")
	} else {
		return fmt.Sprintf("{%s}", output), nil
	}
}

// SliceStructToJson accepts a slice of struct pointer, then using tagName and excludeTagName to marshal to json array
// To pass in inputSliceStructPtr, convert slice of actual objects at the calling code, using SliceObjectsToSliceInterface()
func SliceStructToJson(inputSliceStructPtr []interface{}, tagName string, excludeTagName string) (jsonArrayOutput string, err error) {
	if len(inputSliceStructPtr) == 0 {
		return "", fmt.Errorf("Input Slice Struct Pointer Nil")
	}

	for _, v := range inputSliceStructPtr {
		if s, e := StructToJson(v, tagName, excludeTagName); e != nil {
			return "", fmt.Errorf("StructToJson Failed: %s", e)
		} else {
			if LenTrim(jsonArrayOutput) > 0 {
				jsonArrayOutput += ", "
			}

			jsonArrayOutput += s
		}
	}

	if LenTrim(jsonArrayOutput) > 0 {
		return fmt.Sprintf("[%s]", jsonArrayOutput), nil
	} else {
		return "", fmt.Errorf("SliceStructToJson Yielded Blank String")
	}
}