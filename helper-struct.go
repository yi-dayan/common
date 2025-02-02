package helper

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"
)

/*
 * Copyright 2020-2021 Aldelo, LP
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

// src and dst both must be struct，and dst must be point
// it will copy the src struct with same tag name as dst struct tag
func Fill(src interface{}, dst interface{}) error {
	srcType := reflect.TypeOf(src)
	srcValue := reflect.ValueOf(src)
	dstValue := reflect.ValueOf(dst)

	if srcType.Kind() != reflect.Struct {
		return errors.New("src must be struct")
	}
	if dstValue.Kind() != reflect.Ptr {
		return errors.New("dst must be point")
	}

	for i := 0; i < srcType.NumField(); i++ {
		dstField := dstValue.Elem().FieldByName(srcType.Field(i).Name)
		if dstField.CanSet() {
			dstField.Set(srcValue.Field(i))
		}
	}

	return nil
}

// MarshalStructToQueryParams marshals a struct pointer's fields to query params string,
// output query param names are based on values given in tagName,
// to exclude certain struct fields from being marshaled, use - as value in struct tag defined by tagName,
// if there is a need to name the value of tagName, but still need to exclude from output, use the excludeTagName with -, such as `x:"-"`
//
// special struct tags:
//		1) `getter:"Key"`			// if field type is custom struct or enum,
//									   specify the custom method getter (no parameters allowed) that returns the expected value in first ordinal result position
//									   NOTE: if the method to invoke resides at struct level, precede the method name with 'base.', for example, 'base.XYZ' where XYZ is method name to invoke
//									   NOTE: if the method is to receive a parameter value, always in string data type, add '(x)' after the method name, such as 'XYZ(x)' or 'base.XYZ(x)'
//		2) `booltrue:"1"` 			// if field is defined, contains bool literal for true condition, such as 1 or true, that overrides default system bool literal value,
//									   if bool literal value is determined by existence of outprefix and itself is blank, place a space in both booltrue and boolfalse (setting blank will negate literal override)
//		3) `boolfalse:"0"`			// if field is defined, contains bool literal for false condition, such as 0 or false, that overrides default system bool literal value
//									   if bool literal value is determined by existence of outprefix and itself is blank, place a space in both booltrue and boolfalse (setting blank will negate literal override)
// 		4) `uniqueid:"xyz"`			// if two or more struct field is set with the same uniqueid, then only the first encountered field with the same uniqueid will be used in marshal
//		5) `skipblank:"false"`		// if true, then any fields that is blank string will be excluded from marshal (this only affects fields that are string)
//		6) `skipzero:"false"`		// if true, then any fields that are 0, 0.00, time.Zero(), false, nil will be excluded from marshal (this only affects fields that are number, bool, time, pointer)
//		7) `timeformat:"20060102"`	// for time.Time field, optional date time format, specified as:
//											2006, 06 = year,
//											01, 1, Jan, January = month,
//											02, 2, _2 = day (_2 = width two, right justified)
//											03, 3, 15 = hour (15 = 24 hour format)
//											04, 4 = minute
//											05, 5 = second
//											PM pm = AM PM
//		8) `outprefix:""`			// for marshal method, if field value is to precede with an output prefix, such as XYZ= (affects marshal queryParams / csv methods only)
// 		9) `zeroblank:"false"`		// set true to set blank to data when value is 0, 0.00, or time.IsZero
func MarshalStructToQueryParams(inputStructPtr interface{}, tagName string, excludeTagName string) (string, error) {
	if inputStructPtr == nil {
		return "", fmt.Errorf("MarshalStructToQueryParams Requires Input Struct Variable Pointer")
	}

	if LenTrim(tagName) == 0 {
		return "", fmt.Errorf("MarshalStructToQueryParams Requires TagName (Tag Name defines query parameter name)")
	}

	s := reflect.ValueOf(inputStructPtr)

	if s.Kind() != reflect.Ptr {
		return "", fmt.Errorf("MarshalStructToQueryParams Expects inputStructPtr To Be a Pointer")
	} else {
		s = s.Elem()
	}

	if s.Kind() != reflect.Struct {
		return "", fmt.Errorf("MarshalStructToQueryParams Requires Struct Object")
	}

	output := ""
	uniqueMap := make(map[string]string)

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() {
			tag := field.Tag.Get(tagName)

			if LenTrim(tag) == 0 {
				tag = field.Name
			}

			if tag != "-" {
				if LenTrim(excludeTagName) > 0 {
					if Trim(field.Tag.Get(excludeTagName)) == "-" {
						continue
					}
				}

				if tagUniqueId := Trim(field.Tag.Get("uniqueid")); len(tagUniqueId) > 0 {
					if _, ok := uniqueMap[strings.ToLower(tagUniqueId)]; ok {
						continue
					} else {
						uniqueMap[strings.ToLower(tagUniqueId)] = field.Name
					}
				}

				var boolTrue, boolFalse, timeFormat, outPrefix string
				var skipBlank, skipZero, zeroblank bool

				if vs := GetStructTagsValueSlice(field, "booltrue", "boolfalse", "skipblank", "skipzero", "timeformat", "outprefix", "zeroblank"); len(vs) == 7 {
					boolTrue = vs[0]
					boolFalse = vs[1]
					skipBlank, _ = ParseBool(vs[2])
					skipZero, _ = ParseBool(vs[3])
					timeFormat = vs[4]
					outPrefix = vs[5]
					zeroblank, _ = ParseBool(vs[6])
				}

				oldVal := o

				if tagGetter := Trim(field.Tag.Get("getter")); len(tagGetter) > 0 {
					isBase := false
					useParam := false
					paramVal := ""
					var paramSlice interface{}

					if strings.ToLower(Left(tagGetter, 5)) == "base." {
						isBase = true
						tagGetter = Right(tagGetter, len(tagGetter)-5)
					}

					if strings.ToLower(Right(tagGetter, 3)) == "(x)" {
						useParam = true

						if o.Kind() != reflect.Slice {
							paramVal, _, _ = ReflectValueToString(o, boolTrue, boolFalse, skipBlank, skipZero, timeFormat, zeroblank)
						} else {
							if o.Len() > 0 {
								paramSlice = o.Slice(0, o.Len()).Interface()
							}
						}

						tagGetter = Left(tagGetter, len(tagGetter)-3)
					}

					var ov []reflect.Value
					var notFound bool

					if isBase {
						if useParam {
							if paramSlice == nil {
								ov, notFound = ReflectCall(s.Addr(), tagGetter, paramVal)
							} else {
								ov, notFound = ReflectCall(s.Addr(), tagGetter, paramSlice)
							}
						} else {
							ov, notFound = ReflectCall(s.Addr(), tagGetter)
						}
					} else {
						if useParam {
							if paramSlice == nil {
								ov, notFound = ReflectCall(o, tagGetter, paramVal)
							} else {
								ov, notFound = ReflectCall(o, tagGetter, paramSlice)
							}
						} else {
							ov, notFound = ReflectCall(o, tagGetter)
						}
					}

					if !notFound {
						if len(ov) > 0 {
							o = ov[0]
						}
					}
				}

				if buf, skip, err := ReflectValueToString(o, boolTrue, boolFalse, skipBlank, skipZero, timeFormat, zeroblank); err != nil || skip {
					if tagUniqueId := Trim(field.Tag.Get("uniqueid")); len(tagUniqueId) > 0 {
						if _, ok := uniqueMap[strings.ToLower(tagUniqueId)]; ok {
							delete(uniqueMap, strings.ToLower(tagUniqueId))
						}
					}

					continue
				} else {
					defVal := field.Tag.Get("def")

					if oldVal.Kind() == reflect.Int && oldVal.Int() == 0 && strings.ToLower(buf) == "unknown" {
						// unknown enum value will be serialized as blank
						buf = ""

						if len(defVal) > 0 {
							buf = defVal
						} else {
							if tagUniqueId := Trim(field.Tag.Get("uniqueid")); len(tagUniqueId) > 0 {
								if _, ok := uniqueMap[strings.ToLower(tagUniqueId)]; ok {
									// remove uniqueid if skip
									delete(uniqueMap, strings.ToLower(tagUniqueId))
									continue
								}
							}
						}
					}

					if boolFalse == " " && len(outPrefix) > 0 && buf == "false" {
						buf = ""
					} else {
						if len(buf) == 0 && len(defVal) > 0  {
							buf = defVal
						}

						if skipBlank && LenTrim(buf) == 0 {
							buf = ""
						} else if skipZero && buf == "0" {
							buf = ""
						} else {
							buf = outPrefix + buf
						}
					}

					if LenTrim(output) > 0 {
						output += "&"
					}

					output += fmt.Sprintf("%s=%s", tag, url.PathEscape(buf))
				}
			}
		}
	}

	if LenTrim(output) == 0 {
		return "", fmt.Errorf("MarshalStructToQueryParams Yielded Blank Output")
	} else {
		return output, nil
	}
}

// MarshalStructToJson marshals a struct pointer's fields to json string,
// output json names are based on values given in tagName,
// to exclude certain struct fields from being marshaled, include - as value in struct tag defined by tagName,
// if there is a need to name the value of tagName, but still need to exclude from output, use the excludeTagName with -, such as `x:"-"`
//
// special struct tags:
//		1) `getter:"Key"`			// if field type is custom struct or enum,
//									   specify the custom method getter (no parameters allowed) that returns the expected value in first ordinal result position
//									   NOTE: if the method to invoke resides at struct level, precede the method name with 'base.', for example, 'base.XYZ' where XYZ is method name to invoke
//									   NOTE: if the method is to receive a parameter value, always in string data type, add '(x)' after the method name, such as 'XYZ(x)' or 'base.XYZ(x)'
//		2) `booltrue:"1"` 			// if field is defined, contains bool literal for true condition, such as 1 or true, that overrides default system bool literal value
//									   if bool literal value is determined by existence of outprefix and itself is blank, place a space in both booltrue and boolfalse (setting blank will negate literal override)
//		3) `boolfalse:"0"`			// if field is defined, contains bool literal for false condition, such as 0 or false, that overrides default system bool literal value
//									   if bool literal value is determined by existence of outprefix and itself is blank, place a space in both booltrue and boolfalse (setting blank will negate literal override)
// 		4) `uniqueid:"xyz"`			// if two or more struct field is set with the same uniqueid, then only the first encountered field with the same uniqueid will be used in marshal
//		5) `skipblank:"false"`		// if true, then any fields that is blank string will be excluded from marshal (this only affects fields that are string)
//		6) `skipzero:"false"`		// if true, then any fields that are 0, 0.00, time.Zero(), false, nil will be excluded from marshal (this only affects fields that are number, bool, time, pointer)
//		7) `timeformat:"20060102"`	// for time.Time field, optional date time format, specified as:
//											2006, 06 = year,
//											01, 1, Jan, January = month,
//											02, 2, _2 = day (_2 = width two, right justified)
//											03, 3, 15 = hour (15 = 24 hour format)
//											04, 4 = minute
//											05, 5 = second
//											PM pm = AM PM
// 		8) `zeroblank:"false"`		// set true to set blank to data when value is 0, 0.00, or time.IsZero
func MarshalStructToJson(inputStructPtr interface{}, tagName string, excludeTagName string) (string, error) {
	if inputStructPtr == nil {
		return "", fmt.Errorf("MarshalStructToJson Requires Input Struct Variable Pointer")
	}

	if LenTrim(tagName) == 0 {
		return "", fmt.Errorf("MarshalStructToJson Requires TagName (Tag Name defines Json name)")
	}

	s := reflect.ValueOf(inputStructPtr)

	if s.Kind() != reflect.Ptr {
		return "", fmt.Errorf("MarshalStructToJson Expects inputStructPtr To Be a Pointer")
	} else {
		s = s.Elem()
	}

	if s.Kind() != reflect.Struct {
		return "", fmt.Errorf("MarshalStructToJson Requires Struct Object")
	}

	output := ""
	uniqueMap := make(map[string]string)

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() {
			tag := field.Tag.Get(tagName)

			if LenTrim(tag) == 0 {
				tag = field.Name
			}

			if tag != "-" {
				if LenTrim(excludeTagName) > 0 {
					if Trim(field.Tag.Get(excludeTagName)) == "-" {
						continue
					}
				}

				if tagUniqueId := Trim(field.Tag.Get("uniqueid")); len(tagUniqueId) > 0 {
					if _, ok := uniqueMap[strings.ToLower(tagUniqueId)]; ok {
						continue
					} else {
						uniqueMap[strings.ToLower(tagUniqueId)] = field.Name
					}
				}

				var boolTrue, boolFalse, timeFormat string
				var skipBlank, skipZero, zeroBlank bool

				if vs := GetStructTagsValueSlice(field, "booltrue", "boolfalse", "skipblank", "skipzero", "timeformat", "zeroblank"); len(vs) == 6 {
					boolTrue = vs[0]
					boolFalse = vs[1]
					skipBlank, _ = ParseBool(vs[2])
					skipZero, _ = ParseBool(vs[3])
					timeFormat = vs[4]
					zeroBlank, _ = ParseBool(vs[5])
				}

				oldVal := o

				if tagGetter := Trim(field.Tag.Get("getter")); len(tagGetter) > 0 {
					isBase := false
					useParam := false
					paramVal := ""
					var paramSlice interface{}

					if strings.ToLower(Left(tagGetter, 5)) == "base." {
						isBase = true
						tagGetter = Right(tagGetter, len(tagGetter)-5)
					}

					if strings.ToLower(Right(tagGetter, 3)) == "(x)" {
						useParam = true

						if o.Kind() != reflect.Slice {
							paramVal, _, _ = ReflectValueToString(o, boolTrue, boolFalse, skipBlank, skipZero, timeFormat, zeroBlank)
						} else {
							if o.Len() > 0 {
								paramSlice = o.Slice(0, o.Len()).Interface()
							}
						}

						tagGetter = Left(tagGetter, len(tagGetter)-3)
					}

					var ov []reflect.Value
					var notFound bool

					if isBase {
						if useParam {
							if paramSlice == nil {
								ov, notFound = ReflectCall(s.Addr(), tagGetter, paramVal)
							} else {
								ov, notFound = ReflectCall(s.Addr(), tagGetter, paramSlice)
							}
						} else {
							ov, notFound = ReflectCall(s.Addr(), tagGetter)
						}
					} else {
						if useParam {
							if paramSlice == nil {
								ov, notFound = ReflectCall(o, tagGetter, paramVal)
							} else {
								ov, notFound = ReflectCall(o, tagGetter, paramSlice)
							}
						} else {
							ov, notFound = ReflectCall(o, tagGetter)
						}
					}

					if !notFound {
						if len(ov) > 0 {
							o = ov[0]
						}
					}
				}

				buf, skip, err := ReflectValueToString(o, boolTrue, boolFalse, skipBlank, skipZero, timeFormat, zeroBlank)

				if err != nil || skip {
					if tagUniqueId := Trim(field.Tag.Get("uniqueid")); len(tagUniqueId) > 0 {
						if _, ok := uniqueMap[strings.ToLower(tagUniqueId)]; ok {
							delete(uniqueMap, strings.ToLower(tagUniqueId))
						}
					}

					continue
				}

				defVal := field.Tag.Get("def")

				if oldVal.Kind() == reflect.Int && oldVal.Int() == 0 && strings.ToLower(buf) == "unknown" {
					// unknown enum value will be serialized as blank
					buf = ""

					if len(defVal) > 0 {
						buf = defVal
					} else {
						if tagUniqueId := Trim(field.Tag.Get("uniqueid")); len(tagUniqueId) > 0 {
							if _, ok := uniqueMap[strings.ToLower(tagUniqueId)]; ok {
								// remove uniqueid if skip
								delete(uniqueMap, strings.ToLower(tagUniqueId))
								continue
							}
						}
					}
				}

				outPrefix := field.Tag.Get("outprefix")

				if boolTrue == " " && len(buf) == 0 && len(outPrefix) > 0 {
					buf = outPrefix + defVal
				} else if boolFalse == " " && buf == "false" && len(outPrefix) > 0 {
					buf = ""
				} else if len(defVal) > 0 && len(buf) == 0 {
					buf = outPrefix + defVal
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
		return "", fmt.Errorf("MarshalStructToJson Yielded Blank Output")
	} else {
		return fmt.Sprintf("{%s}", output), nil
	}
}

// UnmarshalJsonToStruct will parse jsonPayload string,
// and set parsed json element value into struct fields based on struct tag named by tagName,
// any tagName value with - will be ignored, any excludeTagName defined with value of - will also cause parser to ignore the field
//
// note: this method expects simple json in key value pairs only, not json containing slices or more complex json structs within existing json field
//
// Predefined Struct Tags Usable:
// 		1) `setter:"ParseByKey`		// if field type is custom struct or enum,
//									   specify the custom method (only 1 lookup parameter value allowed) setter that sets value(s) into the field
//									   NOTE: if the method to invoke resides at struct level, precede the method name with 'base.', for example, 'base.XYZ' where XYZ is method name to invoke
//									   NOTE: setter method always intake a string parameter
//		2) `def:""`					// default value to set into struct field in case unmarshal doesn't set the struct field value
//		3) `timeformat:"20060102"`	// for time.Time field, optional date time format, specified as:
//											2006, 06 = year,
//											01, 1, Jan, January = month,
//											02, 2, _2 = day (_2 = width two, right justified)
//											03, 3, 15 = hour (15 = 24 hour format)
//											04, 4 = minute
//											05, 5 = second
//											PM pm = AM PM
//		4) `booltrue:"1"` 			// if field is defined, contains bool literal for true condition, such as 1 or true, that overrides default system bool literal value,
//									   if bool literal value is determined by existence of outprefix and itself is blank, place a space in both booltrue and boolfalse (setting blank will negate literal override)
//		5) `boolfalse:"0"`			// if field is defined, contains bool literal for false condition, such as 0 or false, that overrides default system bool literal value
//									   if bool literal value is determined by existence of outprefix and itself is blank, place a space in both booltrue and boolfalse (setting blank will negate literal override)
func UnmarshalJsonToStruct(inputStructPtr interface{}, jsonPayload string, tagName string, excludeTagName string) error {
	if inputStructPtr == nil {
		return fmt.Errorf("InputStructPtr is Required")
	}

	if LenTrim(jsonPayload) == 0 {
		return fmt.Errorf("JsonPayload is Required")
	}

	if LenTrim(tagName) == 0 {
		return fmt.Errorf("TagName is Required")
	}

	s := reflect.ValueOf(inputStructPtr)

	if s.Kind() != reflect.Ptr {
		return fmt.Errorf("InputStructPtr Must Be Pointer")
	} else {
		s = s.Elem()
	}

	if s.Kind() != reflect.Struct {
		return fmt.Errorf("InputStructPtr Must Be Struct")
	}

	// unmarshal json to map
	jsonMap := make(map[string]json.RawMessage)

	if err := json.Unmarshal([]byte(jsonPayload), &jsonMap); err != nil {
		return fmt.Errorf("Unmarshal Json Failed: %s", err)
	}

	if jsonMap == nil {
		return fmt.Errorf("Unmarshaled Json Map is Nil")
	}

	if len(jsonMap) == 0 {
		return fmt.Errorf("Unmarshaled Json Map Has No Elements")
	}

	StructClearFields(inputStructPtr)
	SetStructFieldDefaultValues(inputStructPtr)

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() && o.CanSet() {
			// get json field name if defined
			jName := Trim(field.Tag.Get(tagName))

			if jName == "-" {
				continue
			}

			if LenTrim(excludeTagName) > 0 {
				if Trim(field.Tag.Get(excludeTagName)) == "-" {
					continue
				}
			}

			if LenTrim(jName) == 0 {
				jName = field.Name
			}

			// get json field value based on jName from jsonMap
			jValue := ""
			timeFormat := Trim(field.Tag.Get("timeformat"))

			if jRaw, ok := jsonMap[jName]; !ok {
				continue
			} else {
				jValue = JsonFromEscaped(string(jRaw))

				if len(jValue) > 0 {
					if tagSetter := Trim(field.Tag.Get("setter")); len(tagSetter) > 0 {
						isBase := false

						if strings.ToLower(Left(tagSetter, 5)) == "base." {
							isBase = true
							tagSetter = Right(tagSetter, len(tagSetter)-5)
						}

						if o.Kind() != reflect.Ptr && o.Kind() != reflect.Interface && o.Kind() != reflect.Struct && o.Kind() != reflect.Slice {
							// o is not ptr, interface, struct
							var results []reflect.Value
							var notFound bool

							if isBase {
								results, notFound = ReflectCall(s.Addr(), tagSetter, jValue)
							} else {
								results, notFound = ReflectCall(o, tagSetter, jValue)
							}

							if !notFound && len(results) > 0 {
								if len(results) == 1 {
									if jv, _, err := ReflectValueToString(results[0], "", "", false, false, timeFormat, false); err == nil {
										jValue = jv
									}
								} else if len(results) > 1 {
									getFirstVar := true

									if e, ok := results[len(results)-1].Interface().(error); ok {
										// last var is error, check if error exists
										if e != nil {
											getFirstVar = false
										}
									}

									if getFirstVar {
										if jv, _, err := ReflectValueToString(results[0], "", "", false, false, timeFormat, false); err == nil {
											jValue = jv
										}
									}
								}
							}
						} else {
							// o is ptr, interface, struct
							// get base type
							if o.Kind() != reflect.Slice {
								if baseType, _, isNilPtr := DerefPointersZero(o); isNilPtr {
									// create new struct pointer
									o.Set(reflect.New(baseType.Type()))
								} else {
									if o.Kind() == reflect.Interface && o.Interface() == nil {
										customType := ReflectTypeRegistryGet(o.Type().String())

										if customType == nil {
											return fmt.Errorf("%s Struct Field %s is Interface Without Actual Object Assignment", s.Type(), o.Type())
										} else {
											o.Set(reflect.New(customType))
										}
									}
								}
							}

							var ov []reflect.Value
							var notFound bool

							if isBase {
								ov, notFound = ReflectCall(s.Addr(), tagSetter, jValue)
							} else {
								ov, notFound = ReflectCall(o, tagSetter, jValue)
							}

							if !notFound {
								if len(ov) == 1 {
									if ov[0].Kind() == reflect.Ptr || ov[0].Kind() == reflect.Slice {
										o.Set(ov[0])
									}
								} else if len(ov) > 1 {
									getFirstVar := true

									if e := DerefError(ov[len(ov)-1]); e != nil {
										getFirstVar = false
									}

									if getFirstVar {
										if ov[0].Kind() == reflect.Ptr || ov[0].Kind() == reflect.Slice {
											o.Set(ov[0])
										}
									}
								}
							}

							// for o as ptr
							// once complete, continue
							continue
						}
					}
				}
			}

			// set validated csv value into corresponding struct field
			outPrefix := field.Tag.Get("outprefix")
			boolTrue := field.Tag.Get("booltrue")
			boolFalse := field.Tag.Get("boolfalse")

			if boolTrue == " " && len(outPrefix) > 0 && jValue == outPrefix {
				jValue = "true"
			} else {
				evalOk := false
				if LenTrim(boolTrue) > 0 && len(jValue) > 0 && boolTrue == jValue {
					jValue = "true"
					evalOk = true
				}

				if !evalOk {
					if LenTrim(boolFalse) > 0 && len(jValue) > 0 && boolFalse == jValue {
						jValue = "false"
					}
				}
			}

			if err := ReflectStringToField(o, jValue, timeFormat); err != nil {
				return err
			}
		}
	}

	return nil
}

// MarshalSliceStructToJson accepts a slice of struct pointer, then using tagName and excludeTagName to marshal to json array
// To pass in inputSliceStructPtr, convert slice of actual objects at the calling code, using SliceObjectsToSliceInterface(),
// if there is a need to name the value of tagName, but still need to exclude from output, use the excludeTagName with -, such as `x:"-"`
func MarshalSliceStructToJson(inputSliceStructPtr []interface{}, tagName string, excludeTagName string) (jsonArrayOutput string, err error) {
	if len(inputSliceStructPtr) == 0 {
		return "", fmt.Errorf("Input Slice Struct Pointer Nil")
	}

	for _, v := range inputSliceStructPtr {
		if s, e := MarshalStructToJson(v, tagName, excludeTagName); e != nil {
			return "", fmt.Errorf("MarshalSliceStructToJson Failed: %s", e)
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
		return "", fmt.Errorf("MarshalSliceStructToJson Yielded Blank String")
	}
}

// StructClearFields will clear all fields within struct with default value
func StructClearFields(inputStructPtr interface{}) {
	if inputStructPtr == nil {
		return
	}

	s := reflect.ValueOf(inputStructPtr)

	if s.Kind() != reflect.Ptr {
		return
	} else {
		s = s.Elem()
	}

	if s.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() && o.CanSet() {
			switch o.Kind() {
			case reflect.String:
				o.SetString("")
			case reflect.Bool:
				o.SetBool(false)
			case reflect.Int8:
				fallthrough
			case reflect.Int16:
				fallthrough
			case reflect.Int:
				fallthrough
			case reflect.Int32:
				fallthrough
			case reflect.Int64:
				o.SetInt(0)
			case reflect.Float32:
				fallthrough
			case reflect.Float64:
				o.SetFloat(0)
			case reflect.Uint8:
				fallthrough
			case reflect.Uint16:
				fallthrough
			case reflect.Uint:
				fallthrough
			case reflect.Uint32:
				fallthrough
			case reflect.Uint64:
				o.SetUint(0)
			case reflect.Ptr:
				o.Set(reflect.Zero(o.Type()))
			case reflect.Slice:
				o.Set(reflect.Zero(o.Type()))
			default:
				switch o.Interface().(type) {
				case sql.NullString:
					o.Set(reflect.ValueOf(sql.NullString{}))
				case sql.NullBool:
					o.Set(reflect.ValueOf(sql.NullBool{}))
				case sql.NullFloat64:
					o.Set(reflect.ValueOf(sql.NullFloat64{}))
				case sql.NullInt32:
					o.Set(reflect.ValueOf(sql.NullInt32{}))
				case sql.NullInt64:
					o.Set(reflect.ValueOf(sql.NullInt64{}))
				case sql.NullTime:
					o.Set(reflect.ValueOf(sql.NullTime{}))
				case time.Time:
					o.Set(reflect.ValueOf(time.Time{}))
				default:
					o.Set(reflect.Zero(o.Type()))
				}
			}
		}
	}
}

// StructNonDefaultRequiredFieldsCount returns count of struct fields that are tagged as required but not having any default values pre-set
func StructNonDefaultRequiredFieldsCount(inputStructPtr interface{}) int {
	if inputStructPtr == nil {
		return 0
	}

	s := reflect.ValueOf(inputStructPtr)

	if s.Kind() != reflect.Ptr {
		return 0
	} else {
		s = s.Elem()
	}

	if s.Kind() != reflect.Struct {
		return 0
	}

	count := 0

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() && o.CanSet() {
			tagDef := field.Tag.Get("def")
			tagReq := field.Tag.Get("req")

			if len(tagDef) == 0 && strings.ToLower(tagReq) == "true" {
				// required and no default value
				count++
			}
		}
	}

	return count
}

// IsStructFieldSet checks if any field value is not default blank or zero
func IsStructFieldSet(inputStructPtr interface{}) bool {
	if inputStructPtr == nil {
		return false
	}

	s := reflect.ValueOf(inputStructPtr)

	if s.Kind() != reflect.Ptr {
		return false
	} else {
		s = s.Elem()
	}

	if s.Kind() != reflect.Struct {
		return false
	}

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() && o.CanSet() {
			tagDef := field.Tag.Get("def")

			switch o.Kind() {
			case reflect.String:
				if LenTrim(o.String()) > 0 {
					if o.String() != tagDef	{
						return true
					}
				}
			case reflect.Bool:
				if o.Bool() {
					return true
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
				if o.Int() != 0 {
					if Int64ToString(o.Int()) != tagDef	{
						return true
					}
				}
			case reflect.Float32:
				fallthrough
			case reflect.Float64:
				if o.Float() != 0 {
					if Float64ToString(o.Float()) != tagDef	{
						return true
					}
				}
			case reflect.Uint8:
				fallthrough
			case reflect.Uint16:
				fallthrough
			case reflect.Uint:
				fallthrough
			case reflect.Uint32:
				fallthrough
			case reflect.Uint64:
				if o.Uint() > 0 {
					if UInt64ToString(o.Uint()) != tagDef {
						return true
					}
				}
			case reflect.Ptr:
				if !o.IsNil() {
					return true
				}
			case reflect.Slice:
				if o.Len() > 0 {
					return true
				}
			default:
				switch f := o.Interface().(type) {
				case sql.NullString:
					if f.Valid {
						if len(tagDef) == 0 {
							return true
						} else {
							if f.String != tagDef {
								return true
							}
						}
					}
				case sql.NullBool:
					if f.Valid {
						if len(tagDef) == 0 {
							return true
						} else {
							if f.Bool, _ = ParseBool(tagDef); f.Bool {
								return true
							}
						}
					}
				case sql.NullFloat64:
					if f.Valid {
						if len(tagDef) == 0 {
							return true
						} else {
							if Float64ToString(f.Float64) != tagDef {
								return true
							}
						}
					}
				case sql.NullInt32:
					if f.Valid {
						if len(tagDef) == 0 {
							return true
						} else {
							if Itoa(int(f.Int32)) != tagDef {
								return true
							}
						}
					}
				case sql.NullInt64:
					if f.Valid {
						if len(tagDef) == 0 {
							return true
						} else {
							if Int64ToString(f.Int64) != tagDef {
								return true
							}
						}
					}
				case sql.NullTime:
					if f.Valid {
						if len(tagDef) == 0 {
							return true
						} else {
							tagTimeFormat := Trim(field.Tag.Get("timeformat"))

							if LenTrim(tagTimeFormat) == 0 {
								tagTimeFormat = DateTimeFormatString()
							}

							if f.Time != ParseDateTimeCustom(tagDef, tagTimeFormat) {
								return true
							}
						}
					}
				case time.Time:
					if !f.IsZero() {
						if len(tagDef) == 0 {
							return true
						} else {
							tagTimeFormat := Trim(field.Tag.Get("timeformat"))

							if LenTrim(tagTimeFormat) == 0 {
								tagTimeFormat = DateTimeFormatString()
							}

							if f != ParseDateTimeCustom(tagDef, tagTimeFormat) {
								return true
							}
						}
					}
				default:
					if o.Kind() == reflect.Interface && o.Interface() != nil {
						return true
					}
				}
			}
		}
	}

	return false
}

// SetStructFieldDefaultValues sets default value defined in struct tag `def:""` into given field,
// this method is used during unmarshal action only,
// default value setting is for value types and fields with `setter:""` defined only,
// timeformat is used if field is datetime, for overriding default format of ISO style
func SetStructFieldDefaultValues(inputStructPtr interface{}) bool {
	if inputStructPtr == nil {
		return false
	}

	s := reflect.ValueOf(inputStructPtr)

	if s.Kind() != reflect.Ptr {
		return false
	} else {
		s = s.Elem()
	}

	if s.Kind() != reflect.Struct {
		return false
	}

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() && o.CanSet() {
			tagDef := field.Tag.Get("def")

			if len(tagDef) == 0 {
				continue
			}

			switch o.Kind() {
			case reflect.String:
				if LenTrim(o.String()) == 0 {
					o.SetString(tagDef)
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
				if o.Int() == 0 {
					tagSetter := Trim(field.Tag.Get("setter"))

					if LenTrim(tagSetter) == 0 {
						if i64, ok := ParseInt64(tagDef); ok && i64 != 0 {
							if !o.OverflowInt(i64) {
								o.SetInt(i64)
							}
						}
					} else {
						if res, notFound := ReflectCall(o, tagSetter, tagDef); !notFound {
							if len(res) == 1 {
								if val, skip, err := ReflectValueToString(res[0], "", "", false, false, "", false); err == nil && !skip {
									tagDef = val
								} else {
									continue
								}
							} else if len(res) > 1 {
								if err := DerefError(res[len(res)-1:][0]); err == nil {
									if val, skip, err := ReflectValueToString(res[0], "", "", false, false, "", false); err == nil && !skip {
										tagDef = val
									} else {
										continue
									}
								}
							}

							if i64, ok := ParseInt64(tagDef); ok && i64 != 0 {
								if !o.OverflowInt(i64) {
									o.SetInt(i64)
								}
							}
						}
					}
				}
			case reflect.Float32:
				fallthrough
			case reflect.Float64:
				if o.Float() == 0 {
					if f64, ok := ParseFloat64(tagDef); ok && f64 != 0 {
						if !o.OverflowFloat(f64) {
							o.SetFloat(f64)
						}
					}
				}
			case reflect.Uint8:
				fallthrough
			case reflect.Uint16:
				fallthrough
			case reflect.Uint:
				fallthrough
			case reflect.Uint32:
				fallthrough
			case reflect.Uint64:
				if o.Uint() == 0 {
					if u64 := StrToUint64(tagDef); u64 != 0 {
						if !o.OverflowUint(u64) {
							o.SetUint(u64)
						}
					}
				}
			default:
				switch f := o.Interface().(type) {
				case sql.NullString:
					if !f.Valid {
						o.Set(reflect.ValueOf(sql.NullString{String: tagDef, Valid: true}))
					}
				case sql.NullBool:
					if !f.Valid {
						b, _ := ParseBool(tagDef)
						o.Set(reflect.ValueOf(sql.NullBool{Bool: b, Valid: true}))
					}
				case sql.NullFloat64:
					if !f.Valid {
						if f64, ok := ParseFloat64(tagDef); ok && f64 != 0 {
							o.Set(reflect.ValueOf(sql.NullFloat64{Float64: f64, Valid: true}))
						}
					}
				case sql.NullInt32:
					if !f.Valid {
						if i32, ok := ParseInt32(tagDef); ok && i32 != 0 {
							o.Set(reflect.ValueOf(sql.NullInt32{Int32: int32(i32), Valid: true}))
						}
					}
				case sql.NullInt64:
					if !f.Valid {
						if i64, ok := ParseInt64(tagDef); ok && i64 != 0 {
							o.Set(reflect.ValueOf(sql.NullInt64{Int64: i64, Valid: true}))
						}
					}
				case sql.NullTime:
					if !f.Valid {
						tagTimeFormat := Trim(field.Tag.Get("timeformat"))

						if LenTrim(tagTimeFormat) == 0 {
							tagTimeFormat = DateTimeFormatString()
						}

						if t := ParseDateTimeCustom(tagDef, tagTimeFormat); !t.IsZero() {
							o.Set(reflect.ValueOf(sql.NullTime{Time: t, Valid: true}))
						}
					}
				case time.Time:
					if f.IsZero() {
						tagTimeFormat := Trim(field.Tag.Get("timeformat"))

						if LenTrim(tagTimeFormat) == 0 {
							tagTimeFormat = DateTimeFormatString()
						}

						if t := ParseDateTimeCustom(tagDef, tagTimeFormat); !t.IsZero() {
							o.Set(reflect.ValueOf(t))
						}
					}
				}
			}
		}
	}

	return true
}

// UnmarshalCSVToStruct will parse csvPayload string (one line of csv data) using csvDelimiter, (if csvDelimiter = "", then customDelimiterParserFunc is required)
// and set parsed csv element value into struct fields based on Ordinal Position defined via struct tag,
// additionally processes struct tag data validation and length / range (if not valid, will set to data type default)
//
// Predefined Struct Tags Usable:
//		1) `pos:"1"`				// ordinal position of the field in relation to the csv parsed output expected (Zero-Based Index)
//									   NOTE: if field is mutually exclusive with one or more uniqueId, then pos # should be named the same for all uniqueIds,
//											 if multiple fields are in exclusive condition, and skipBlank or skipZero, always include a blank default field as the last of unique field list
//										     if value is '-', this means position value is calculated from other fields and set via `setter:"base.Xyz"` during unmarshal csv, there is no marshal to csv for this field
//		2) `type:"xyz"`				// data type expected:
//											A = AlphabeticOnly, N = NumericOnly 0-9, AN = AlphaNumeric, ANS = AN + PrintableSymbols,
//											H = Hex, B64 = Base64, B = true/false, REGEX = Regular Expression, Blank = Any,
//		3) `regex:"xyz"`			// if Type = REGEX, this struct tag contains the regular expression string,
//										 	regex express such as [^A-Za-z0-9_-]+
//										 	method will replace any regex matched string to blank
//		4) `size:"x..y"`			// data type size rule:
//											x = Exact size match
//											x.. = From x and up
//											..y = From 0 up to y
//											x..y = From x to y
//											+%z = Append to x, x.., ..y, x..y; adds additional constraint that the result size must equate to 0 from modulo of z
//		5) `range:"x..y"`			// data type range value when Type is N, if underlying data type is string, method will convert first before testing
//		6) `req:"true"`				// indicates data value is required or not, true or false
//		7) `getter:"Key"`			// if field type is custom struct or enum, specify the custom method getter (no parameters allowed) that returns the expected value in first ordinal result position
//									   NOTE: if the method to invoke resides at struct level, precede the method name with 'base.', for example, 'base.XYZ' where XYZ is method name to invoke
//									   NOTE: if the method is to receive a parameter value, always in string data type, add '(x)' after the method name, such as 'XYZ(x)' or 'base.XYZ(x)'
// 		8) `setter:"ParseByKey`		// if field type is custom struct or enum, specify the custom method (only 1 lookup parameter value allowed) setter that sets value(s) into the field
//									   NOTE: if the method to invoke resides at struct level, precede the method name with 'base.', for example, 'base.XYZ' where XYZ is method name to invoke
//									   NOTE: setter method always intake a string parameter value
//		9) `outprefix:""`			// for marshal method, if field value is to precede with an output prefix, such as XYZ= (affects marshal queryParams / csv methods only)
//									   WARNING: if csv is variable elements count, rather than fixed count ordinal, then csv MUST include outprefix for all fields in order to properly identify target struct field
//		10) `def:""`				// default value to set into struct field in case unmarshal doesn't set the struct field value
//		11) `timeformat:"20060102"`	// for time.Time field, optional date time format, specified as:
//											2006, 06 = year,
//											01, 1, Jan, January = month,
//											02, 2, _2 = day (_2 = width two, right justified)
//											03, 3, 15 = hour (15 = 24 hour format)
//											04, 4 = minute
//											05, 5 = second
//											PM pm = AM PM
//		12) `booltrue:"1"` 			// if field is defined, contains bool literal for true condition, such as 1 or true, that overrides default system bool literal value,
//									   if bool literal value is determined by existence of outprefix and itself is blank, place a space in both booltrue and boolfalse (setting blank will negate literal override)
//		13) `boolfalse:"0"`			// if field is defined, contains bool literal for false condition, such as 0 or false, that overrides default system bool literal value
//									   if bool literal value is determined by existence of outprefix and itself is blank, place a space in both booltrue and boolfalse (setting blank will negate literal override)
//		14) `validate:"==x"`		// if field has to match a specific value or the entire method call will fail, match data format as:
//									   		==xyz (== refers to equal, for numbers and string match, xyz is data to match, case insensitive)
//												[if == validate against one or more values, use ||]
//									   		!=xyz (!= refers to not equal)
//												[if != validate against one or more values, use &&]
//											>=xyz >>xyz <<xyz <=xyz (greater equal, greater, less than, less equal; xyz must be int or float)
//											:=Xyz where Xyz is a parameterless function defined at struct level, that performs validation, returns bool or error where true or nil indicates validation success
//									   note: expected source data type for validate to be effective is string, int, float64; if field is blank and req = false, then validate will be skipped
func UnmarshalCSVToStruct(inputStructPtr interface{}, csvPayload string, csvDelimiter string, customDelimiterParserFunc func(string) []string) error {
	if inputStructPtr == nil {
		return fmt.Errorf("InputStructPtr is Required")
	}

	if LenTrim(csvPayload) == 0 {
		return fmt.Errorf("CSV Payload is Required")
	}

	if len(csvDelimiter) == 0 && customDelimiterParserFunc == nil {
		return fmt.Errorf("CSV Delimiter or Custom Delimiter Func is Required")
	}

	s := reflect.ValueOf(inputStructPtr)

	if s.Kind() != reflect.Ptr {
		return fmt.Errorf("InputStructPtr Must Be Pointer")
	} else {
		s = s.Elem()
	}

	if s.Kind() != reflect.Struct {
		return fmt.Errorf("InputStructPtr Must Be Struct")
	}

	trueList := []string{"true", "yes", "on", "1", "enabled"}

	var csvElements []string

	if len(csvDelimiter) > 0 {
		csvElements = strings.Split(csvPayload, csvDelimiter)
	} else {
		csvElements = customDelimiterParserFunc(csvPayload)
	}

	csvLen := len(csvElements)

	if csvLen == 0 {
		return fmt.Errorf("CSV Payload Contains Zero Elements")
	}

	StructClearFields(inputStructPtr)
	SetStructFieldDefaultValues(inputStructPtr)
	prefixProcessedMap := make(map[string]string)

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() && o.CanSet() {
			// extract struct tag values
			tagPosBuf := field.Tag.Get("pos")
			tagPos, ok := ParseInt32(tagPosBuf)
			if !ok {
				if tagPosBuf != "-" || LenTrim(field.Tag.Get("setter")) == 0 {
					continue
				}
			} else if tagPos < 0 {
				continue
			}

			tagType := Trim(strings.ToLower(field.Tag.Get("type")))
			switch tagType {
			case "a":
				fallthrough
			case "n":
				fallthrough
			case "an":
				fallthrough
			case "ans":
				fallthrough
			case "b":
				fallthrough
			case "b64":
				fallthrough
			case "regex":
				fallthrough
			case "h":
				// valid type
			default:
				tagType = ""
			}

			tagRegEx := Trim(field.Tag.Get("regex"))
			if tagType != "regex" {
				tagRegEx = ""
			} else {
				if LenTrim(tagRegEx) == 0 {
					tagType = ""
				}
			}

			// unmarshal only validates max
			tagSize := Trim(strings.ToLower(field.Tag.Get("size")))
			arModulo := strings.Split(tagSize, "+%")
			tagModulo := 0
			if len(arModulo) == 2 {
				tagSize = arModulo[0]
				if tagModulo, _ = ParseInt32(arModulo[1]); tagModulo < 0 {
					tagModulo = 0
				}
			}
			arSize := strings.Split(tagSize, "..")
			sizeMin := 0
			sizeMax := 0
			if len(arSize) == 2 {
				sizeMin, _ = ParseInt32(arSize[0])
				sizeMax, _ = ParseInt32(arSize[1])
			} else {
				sizeMin, _ = ParseInt32(tagSize)
				sizeMax = sizeMin
			}

			/*
			// tagRange not used in unmarshal
			tagRange := Trim(strings.ToLower(field.Tag.Get("range")))
			arRange := strings.Split(tagRange, "..")
			rangeMin := 0
			rangeMax := 0
			if len(arRange) == 2 {
				rangeMin, _ = ParseInt32(arRange[0])
				rangeMax, _ = ParseInt32(arRange[1])
			} else {
				rangeMin, _ = ParseInt32(tagRange)
				rangeMax = rangeMin
			}
			*/

			// tagReq not used in unmarshal
			tagReq := Trim(strings.ToLower(field.Tag.Get("req")))
			if tagReq != "true" && tagReq != "false" {
				tagReq = ""
			}

			// if outPrefix exists, remove from csvValue
			outPrefix := Trim(field.Tag.Get("outprefix"))

			// get csv value by ordinal position
			csvValue := ""

			if tagPosBuf != "-" {
				if LenTrim(outPrefix) == 0 {
					// ordinal based csv parsing
					if csvElements != nil {
						if tagPos > csvLen-1 {
							// no more elements to unmarshal, rest of fields using default values
							return nil
						} else {
							csvValue = csvElements[tagPos]

							evalOk := false
							if boolTrue := Trim(field.Tag.Get("booltrue")); len(boolTrue) > 0 {
								if boolTrue == csvValue {
									csvValue = "true"
									evalOk = true
								}
							}

							if !evalOk {
								if boolFalse := Trim(field.Tag.Get("boolfalse")); len(boolFalse) > 0 {
									if boolFalse == csvValue {
										csvValue = "false"
									}
								}
							}
						}
					}
				} else {
					// variable element based csv, using outPrefix as the identifying key
					// instead of getting csv value from element position, acquire from outPrefix
					notFound := true

					for _, v := range csvElements {
						if strings.ToLower(Left(v, len(outPrefix))) == strings.ToLower(outPrefix) {
							// match
							if _, ok := prefixProcessedMap[strings.ToLower(outPrefix)]; !ok {
								prefixProcessedMap[strings.ToLower(outPrefix)] = Itoa(tagPos)

								if len(v)-len(outPrefix) == 0 {
									csvValue = ""

									if field.Tag.Get("booltrue") == " " {
										// prefix found, since data is blank, and boolTrue is space, treat this as true
										csvValue = "true"
									}
								} else {
									csvValue = Right(v, len(v)-len(outPrefix))

									evalOk := false
									if boolTrue := Trim(field.Tag.Get("booltrue")); len(boolTrue) > 0 {
										if boolTrue == csvValue {
											csvValue = "true"
											evalOk = true
										}
									}

									if !evalOk {
										if boolFalse := Trim(field.Tag.Get("boolfalse")); len(boolFalse) > 0 {
											if boolFalse == csvValue {
												csvValue = "false"
											}
										}
									}
								}

								notFound = false
								break
							}
						}
					}

					if notFound {
						continue
					}
				}
			}

			// pre-process csv value with validation
			tagSetter := Trim(field.Tag.Get("setter"))
			hasSetter := false

			isBase := false
			if LenTrim(tagSetter) > 0 {
				hasSetter = true

				if strings.ToLower(Left(tagSetter, 5)) == "base." {
					isBase = true
					tagSetter = Right(tagSetter, len(tagSetter)-5)
				}
			}

			timeFormat := Trim(field.Tag.Get("timeformat"))

			if o.Kind() != reflect.Ptr && o.Kind() != reflect.Interface && o.Kind() != reflect.Struct && o.Kind() != reflect.Slice {
				if tagPosBuf != "-" {
					switch tagType {
					case "a":
						csvValue, _ = ExtractAlpha(csvValue)
					case "n":
						csvValue, _ = ExtractNumeric(csvValue)
					case "an":
						csvValue, _ = ExtractAlphaNumeric(csvValue)
					case "ans":
						if !hasSetter {
							csvValue, _ = ExtractAlphaNumericPrintableSymbols(csvValue)
						}
					case "b":
						if StringSliceContains(&trueList, strings.ToLower(csvValue)) {
							csvValue = "true"
						} else {
							csvValue = "false"
						}
					case "regex":
						csvValue, _ = ExtractByRegex(csvValue, tagRegEx)
					case "h":
						csvValue, _ = ExtractHex(csvValue)
					case "b64":
						csvValue, _ = ExtractAlphaNumericPrintableSymbols(csvValue)
					}

					if tagType == "a" || tagType == "an" || tagType == "ans" || tagType == "n" || tagType == "regex" || tagType == "h" || tagType == "b64" {
						if sizeMax > 0 {
							if len(csvValue) > sizeMax {
								csvValue = Left(csvValue, sizeMax)
							}
						}

						if tagModulo > 0 {
							if len(csvValue)%tagModulo != 0 {
								return fmt.Errorf("Struct Field %s Expects Value In Blocks of %d Characters", field.Name, tagModulo)
							}
						}
					}
				}

				if LenTrim(tagSetter) > 0 {
					var ov []reflect.Value
					var notFound bool

					if isBase {
						ov, notFound = ReflectCall(s.Addr(), tagSetter, csvValue)
					} else {
						ov, notFound = ReflectCall(o, tagSetter, csvValue)
					}

					if !notFound {
						if len(ov) == 1 {
							csvValue, _, _ = ReflectValueToString(ov[0], "", "", false, false, timeFormat, false)
						} else if len(ov) > 1 {
							getFirstVar := true

							if e, ok := ov[len(ov)-1].Interface().(error); ok {
								// last var is error, check if error exists
								if e != nil {
									getFirstVar = false
								}
							}

							if getFirstVar {
								csvValue, _, _ = ReflectValueToString(ov[0], "", "", false, false, timeFormat, false)
							}
						}
					}
				}

				// validate if applicable
				skipFieldSet := false

				if valData := Trim(field.Tag.Get("validate")); len(valData) >= 3 {
					valComp := Left(valData, 2)
					valData = Right(valData, len(valData)-2)

					switch valComp {
					case "==":
						valAr := strings.Split(valData, "||")

						if len(valAr) <= 1 {
							if strings.ToLower(csvValue) != strings.ToLower(valData) {
								if len(csvValue) > 0 || tagReq == "true" {
									StructClearFields(inputStructPtr)
									return fmt.Errorf("%s Validation Failed: Expected To Match '%s', But Received '%s'", field.Name, valData, csvValue)
								}
							}
						} else {
							found := false

							for _, va := range valAr {
								if strings.ToLower(csvValue) == strings.ToLower(va) {
									found = true
									break
								}
							}

							if !found && (len(csvValue) > 0 || tagReq == "true") {
								return fmt.Errorf("%s Validation Failed: Expected To Match '%s', But Received '%s'", field.Name, strings.ReplaceAll(valData, "||", " or "), csvValue)
							}
						}
					case "!=":
						valAr := strings.Split(valData, "&&")

						if len(valAr) <= 1 {
							if strings.ToLower(csvValue) == strings.ToLower(valData) {
								if len(csvValue) > 0 || tagReq == "true" {
									StructClearFields(inputStructPtr)
									return fmt.Errorf("%s Validation Failed: Expected To Not Match '%s', But Received '%s'", field.Name, valData, csvValue)
								}
							}
						} else {
							found := false

							for _, va := range valAr {
								if strings.ToLower(csvValue) == strings.ToLower(va) {
									found = true
									break
								}
							}

							if found && (len(csvValue) > 0 || tagReq == "true") {
								return fmt.Errorf("%s Validation Failed: Expected To Not Match '%s', But Received '%s'", field.Name, strings.ReplaceAll(valData, "&&", " and "), csvValue)
							}
						}
					case "<=":
						if valNum, valOk := ParseFloat64(valData); valOk {
							if srcNum, _ := ParseFloat64(csvValue); srcNum > valNum {
								if len(csvValue) > 0 || tagReq == "true" {
									StructClearFields(inputStructPtr)
									return fmt.Errorf("%s Validation Failed: Expected To Be Less or Equal To '%s', But Received '%s'", field.Name, valData, csvValue)
								}
							}
						}
					case "<<":
						if valNum, valOk := ParseFloat64(valData); valOk {
							if srcNum, _ := ParseFloat64(csvValue); srcNum >= valNum {
								if len(csvValue) > 0 || tagReq == "true" {
									StructClearFields(inputStructPtr)
									return fmt.Errorf("%s Validation Failed: Expected To Be Less Than '%s', But Received '%s'", field.Name, valData, csvValue)
								}
							}
						}
					case ">=":
						if valNum, valOk := ParseFloat64(valData); valOk {
							if srcNum, _ := ParseFloat64(csvValue); srcNum < valNum {
								if len(csvValue) > 0 || tagReq == "true" {
									StructClearFields(inputStructPtr)
									return fmt.Errorf("%s Validation Failed: Expected To Be Greater or Equal To '%s', But Received '%s'", field.Name, valData, csvValue)
								}
							}
						}
					case ">>":
						if valNum, valOk := ParseFloat64(valData); valOk {
							if srcNum, _ := ParseFloat64(csvValue); srcNum <= valNum {
								if len(csvValue) > 0 || tagReq == "true" {
									StructClearFields(inputStructPtr)
									return fmt.Errorf("%s Validation Failed: Expected To Be Greater Than '%s', But Received '%s'", field.Name, valData, csvValue)
								}
							}
						}
					case ":=":
						if len(valData) > 0 {
							skipFieldSet = true

							if err := ReflectStringToField(o, csvValue, timeFormat); err != nil {
								return err
							}

							if retV, nf := ReflectCall(s.Addr(), valData); !nf {
								if len(retV) > 0 {
									if retV[0].Kind() == reflect.Bool && !retV[0].Bool() {
										// validation failed with bool false
										StructClearFields(inputStructPtr)
										return fmt.Errorf("%s Validation Failed: %s() Returned Result is False", field.Name, valData)
									} else if retErr := DerefError(retV[0]); retErr != nil {
										// validation failed with error
										StructClearFields(inputStructPtr)
										return fmt.Errorf("%s Validation On %s() Failed: %s", field.Name, valData, retErr.Error())
									}
								}
							}
						}
					}
				}

				// set validated csv value into corresponding struct field
				if !skipFieldSet {
					if err := ReflectStringToField(o, csvValue, timeFormat); err != nil {
						return err
					}
				}
			} else {
				if LenTrim(tagSetter) > 0 {
					if o.Kind() != reflect.Slice {
						// get base type
						if baseType, _, isNilPtr := DerefPointersZero(o); isNilPtr {
							// create new struct pointer
							o.Set(reflect.New(baseType.Type()))
						} else {
							if o.Kind() == reflect.Interface && o.Interface() == nil {
								customType := ReflectTypeRegistryGet(o.Type().String())

								if customType == nil {
									return fmt.Errorf("%s Struct Field %s is Interface Without Actual Object Assignment", s.Type(), o.Type())
								} else {
									o.Set(reflect.New(customType))
								}
							}
						}
					}

					var ov []reflect.Value
					var notFound bool

					if isBase {
						ov, notFound = ReflectCall(s.Addr(), tagSetter, csvValue)
					} else {
						ov, notFound = ReflectCall(o, tagSetter, csvValue)
					}

					if !notFound {
						if len(ov) == 1 {
							if ov[0].Kind() == reflect.Ptr || ov[0].Kind() == reflect.Slice {
								o.Set(ov[0])
							}
						} else if len(ov) > 1 {
							getFirstVar := true

							if e := DerefError(ov[len(ov)-1]); e != nil {
								getFirstVar = false
							}

							if getFirstVar {
								if ov[0].Kind() == reflect.Ptr || ov[0].Kind() == reflect.Slice {
									o.Set(ov[0])
								}
							}
						}
					}
				} else {
					// set validated csv value into corresponding struct pointer field
					if err := ReflectStringToField(o, csvValue, timeFormat); err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

// MarshalStructToCSV will serialize struct fields defined with strug tags below, to csvPayload string (one line of csv data) using csvDelimiter,
// the csv payload ordinal position is based on the struct tag pos defined for each struct field,
// additionally processes struct tag data validation and length / range (if not valid, will set to data type default),
// this method provides data validation and if fails, will return error (for string if size exceeds max, it will truncate)
//
// Predefined Struct Tags Usable:
//		1) `pos:"1"`				// ordinal position of the field in relation to the csv parsed output expected (Zero-Based Index)
//									   NOTE: if field is mutually exclusive with one or more uniqueId, then pos # should be named the same for all uniqueIds
//											 if multiple fields are in exclusive condition, and skipBlank or skipZero, always include a blank default field as the last of unique field list
//										     if value is '-', this means position value is calculated from other fields and set via `setter:"base.Xyz"` during unmarshal csv, there is no marshal to csv for this field
//		2) `type:"xyz"`				// data type expected:
//											A = AlphabeticOnly, N = NumericOnly 0-9, AN = AlphaNumeric, ANS = AN + PrintableSymbols,
//											H = Hex, B64 = Base64, B = true/false, REGEX = Regular Expression, Blank = Any,
//		3) `regex:"xyz"`			// if Type = REGEX, this struct tag contains the regular expression string,
//										 	regex express such as [^A-Za-z0-9_-]+
//										 	method will replace any regex matched string to blank
//		4) `size:"x..y"`			// data type size rule:
//											x = Exact size match
//											x.. = From x and up
//											..y = From 0 up to y
//											x..y = From x to y
//											+%z = Append to x, x.., ..y, x..y; adds additional constraint that the result size must equate to 0 from modulo of z
//		5) `range:"x..y"`			// data type range value when Type is N, if underlying data type is string, method will convert first before testing
//		6) `req:"true"`				// indicates data value is required or not, true or false
//		7) `getter:"Key"`			// if field type is custom struct or enum, specify the custom method getter (no parameters allowed) that returns the expected value in first ordinal result position
//									   NOTE: if the method to invoke resides at struct level, precede the method name with 'base.', for example, 'base.XYZ' where XYZ is method name to invoke
//									   NOTE: if the method is to receive a parameter value, always in string data type, add '(x)' after the method name, such as 'XYZ(x)' or 'base.XYZ(x)'
// 		8) `setter:"ParseByKey`		// if field type is custom struct or enum, specify the custom method (only 1 lookup parameter value allowed) setter that sets value(s) into the field
//									   NOTE: if the method to invoke resides at struct level, precede the method name with 'base.', for example, 'base.XYZ' where XYZ is method name to invoke
//									   NOTE: setter method always intake a string parameter value
//		9) `booltrue:"1"` 			// if field is defined, contains bool literal for true condition, such as 1 or true, that overrides default system bool literal value,
//									   if bool literal value is determined by existence of outprefix and itself is blank, place a space in both booltrue and boolfalse (setting blank will negate literal override)
//		10) `boolfalse:"0"`			// if field is defined, contains bool literal for false condition, such as 0 or false, that overrides default system bool literal value
//									   if bool literal value is determined by existence of outprefix and itself is blank, place a space in both booltrue and boolfalse (setting blank will negate literal override)
// 		11) `uniqueid:"xyz"`		// if two or more struct field is set with the same uniqueid, then only the first encountered field with the same uniqueid will be used in marshal,
//									   NOTE: if field is mutually exclusive with one or more uniqueId, then pos # should be named the same for all uniqueIds
//		12) `skipblank:"false"`		// if true, then any fields that is blank string will be excluded from marshal (this only affects fields that are string)
//		13) `skipzero:"false"`		// if true, then any fields that are 0, 0.00, time.Zero(), false, nil will be excluded from marshal (this only affects fields that are number, bool, time, pointer)
//		14) `timeformat:"20060102"`	// for time.Time field, optional date time format, specified as:
//											2006, 06 = year,
//											01, 1, Jan, January = month,
//											02, 2, _2 = day (_2 = width two, right justified)
//											03, 3, 15 = hour (15 = 24 hour format)
//											04, 4 = minute
//											05, 5 = second
//											PM pm = AM PM
//		15) `outprefix:""`			// for marshal method, if field value is to precede with an output prefix, such as XYZ= (affects marshal queryParams / csv methods only)
//									   WARNING: if csv is variable elements count, rather than fixed count ordinal, then csv MUST include outprefix for all fields in order to properly identify target struct field
// 		16) `zeroblank:"false"`		// set true to set blank to data when value is 0, 0.00, or time.IsZero
//		17) `validate:"==x"`		// if field has to match a specific value or the entire method call will fail, match data format as:
//									   		==xyz (== refers to equal, for numbers and string match, xyz is data to match, case insensitive)
//												[if == validate against one or more values, use ||]
//									   		!=xyz (!= refers to not equal)
//												[if != validate against one or more values, use &&]
//											>=xyz >>xyz <<xyz <=xyz (greater equal, greater, less than, less equal; xyz must be int or float)
//											:=Xyz where Xyz is a parameterless function defined at struct level, that performs validation, returns bool or error where true or nil indicates validation success
//									   note: expected source data type for validate to be effective is string, int, float64; if field is blank and req = false, then validate will be skipped
func MarshalStructToCSV(inputStructPtr interface{}, csvDelimiter string) (csvPayload string, err error) {
	if inputStructPtr == nil {
		return "", fmt.Errorf("InputStructPtr is Required")
	}

	s := reflect.ValueOf(inputStructPtr)

	if s.Kind() != reflect.Ptr {
		return "", fmt.Errorf("InputStructPtr Must Be Pointer")
	} else {
		s = s.Elem()
	}

	if s.Kind() != reflect.Struct {
		return "", fmt.Errorf("InputStructPtr Must Be Struct")
	}

	if !IsStructFieldSet(inputStructPtr) && StructNonDefaultRequiredFieldsCount(inputStructPtr) > 0 {
		return "", nil
	}

	trueList := []string{"true", "yes", "on", "1", "enabled"}

	csvList := make([]string, s.NumField())
	csvLen := len(csvList)

	for i := 0; i < csvLen; i++ {
		csvList[i] = "{?}"	// indicates value not set, to be excluded
	}

	uniqueMap := make(map[string]string)

	for i := 0; i < s.NumField(); i++ {
		field := s.Type().Field(i)

		if o := s.FieldByName(field.Name); o.IsValid() && o.CanSet() {
			// extract struct tag values
			tagPos, ok := ParseInt32(field.Tag.Get("pos"))
			if !ok {
				continue
			} else if tagPos < 0 {
				continue
			} else if tagPos > csvLen-1 {
				continue
			}

			if tagUniqueId := Trim(field.Tag.Get("uniqueid")); len(tagUniqueId) > 0 {
				if _, ok := uniqueMap[strings.ToLower(tagUniqueId)]; ok {
					continue
				} else {
					uniqueMap[strings.ToLower(tagUniqueId)] = field.Name
				}
			}

			tagType := Trim(strings.ToLower(field.Tag.Get("type")))
			switch tagType {
			case "a":
				fallthrough
			case "n":
				fallthrough
			case "an":
				fallthrough
			case "ans":
				fallthrough
			case "b":
				fallthrough
			case "b64":
				fallthrough
			case "regex":
				fallthrough
			case "h":
				// valid type
			default:
				tagType = ""
			}

			tagRegEx := Trim(field.Tag.Get("regex"))
			if tagType != "regex" {
				tagRegEx = ""
			} else {
				if LenTrim(tagRegEx) == 0 {
					tagType = ""
				}
			}

			tagSize := Trim(strings.ToLower(field.Tag.Get("size")))
			arModulo := strings.Split(tagSize, "+%")
			tagModulo := 0
			if len(arModulo) == 2 {
				tagSize = arModulo[0]
				if tagModulo, _ = ParseInt32(arModulo[1]); tagModulo < 0 {
					tagModulo = 0
				}
			}
			arSize := strings.Split(tagSize, "..")
			sizeMin := 0
			sizeMax := 0
			if len(arSize) == 2 {
				sizeMin, _ = ParseInt32(arSize[0])
				sizeMax, _ = ParseInt32(arSize[1])
			} else {
				sizeMin, _ = ParseInt32(tagSize)
				sizeMax = sizeMin
			}

			tagRange := Trim(strings.ToLower(field.Tag.Get("range")))
			arRange := strings.Split(tagRange, "..")
			rangeMin := 0
			rangeMax := 0
			if len(arRange) == 2 {
				rangeMin, _ = ParseInt32(arRange[0])
				rangeMax, _ = ParseInt32(arRange[1])
			} else {
				rangeMin, _ = ParseInt32(tagRange)
				rangeMax = rangeMin
			}

			tagReq := Trim(strings.ToLower(field.Tag.Get("req")))
			if tagReq != "true" && tagReq != "false" {
				tagReq = ""
			}

			// get csv value from current struct field
			var boolTrue, boolFalse, timeFormat, outPrefix string
			var skipBlank, skipZero, zeroBlank bool

			if vs := GetStructTagsValueSlice(field, "booltrue", "boolfalse", "skipblank", "skipzero", "timeformat", "outprefix", "zeroblank"); len(vs) == 7 {
				boolTrue = vs[0]
				boolFalse = vs[1]
				skipBlank, _ = ParseBool(vs[2])
				skipZero, _ = ParseBool(vs[3])
				timeFormat = vs[4]
				outPrefix = vs[5]
				zeroBlank, _ = ParseBool(vs[6])
			}

			// cache old value prior to getter invoke
			oldVal := o
			hasGetter := false

			if tagGetter := Trim(field.Tag.Get("getter")); len(tagGetter) > 0 {
				hasGetter = true

				isBase := false
				useParam := false
				paramVal := ""
				var paramSlice interface{}

				if strings.ToLower(Left(tagGetter, 5)) == "base." {
					isBase = true
					tagGetter = Right(tagGetter, len(tagGetter)-5)
				}

				if strings.ToLower(Right(tagGetter, 3)) == "(x)" {
					useParam = true

					if o.Kind() != reflect.Slice {
						paramVal, _, _ = ReflectValueToString(o, boolTrue, boolFalse, skipBlank, skipZero, timeFormat, zeroBlank)
					} else {
						if o.Len() > 0 {
							paramSlice = o.Slice(0, o.Len()).Interface()
						}
					}

					tagGetter = Left(tagGetter, len(tagGetter)-3)
				}

				var ov []reflect.Value
				var notFound bool

				if isBase {
					if useParam {
						if paramSlice == nil {
							ov, notFound = ReflectCall(s.Addr(), tagGetter, paramVal)
						} else {
							ov, notFound = ReflectCall(s.Addr(), tagGetter, paramSlice)
						}
					} else {
						ov, notFound = ReflectCall(s.Addr(), tagGetter)
					}
				} else {
					if useParam {
						if paramSlice == nil {
							ov, notFound = ReflectCall(o, tagGetter, paramVal)
						} else {
							ov, notFound = ReflectCall(o, tagGetter, paramSlice)
						}
					} else {
						ov, notFound = ReflectCall(o, tagGetter)
					}
				}

				if !notFound {
					if len(ov) > 0 {
						o = ov[0]
					}
				}
			}

			fv, skip, e := ReflectValueToString(o, boolTrue, boolFalse, skipBlank, skipZero, timeFormat, zeroBlank)

			if e != nil {
				if tagUniqueId := Trim(field.Tag.Get("uniqueid")); len(tagUniqueId) > 0 {
					if _, ok := uniqueMap[strings.ToLower(tagUniqueId)]; ok {
						// remove uniqueid if skip
						delete(uniqueMap, strings.ToLower(tagUniqueId))
					}
				}

				return "", e
			}

			if skip {
				if tagUniqueId := Trim(field.Tag.Get("uniqueid")); len(tagUniqueId) > 0 {
					if _, ok := uniqueMap[strings.ToLower(tagUniqueId)]; ok {
						// remove uniqueid if skip
						delete(uniqueMap, strings.ToLower(tagUniqueId))
					}
				}

				continue
			}

			defVal := field.Tag.Get("def")

			if oldVal.Kind() == reflect.Int && oldVal.Int() == 0 && strings.ToLower(fv) == "unknown" {
				// unknown enum value will be serialized as blank
				fv = ""

				if len(defVal) > 0 {
					fv = defVal
				} else {
					if tagUniqueId := Trim(field.Tag.Get("uniqueid")); len(tagUniqueId) > 0 {
						if _, ok := uniqueMap[strings.ToLower(tagUniqueId)]; ok {
							// remove uniqueid if skip
							delete(uniqueMap, strings.ToLower(tagUniqueId))
							continue
						}
					}
				}
			}

			// validate output csv value
			if oldVal.Kind() != reflect.Slice {
				origFv := fv

				switch tagType {
				case "a":
					fv, _ = ExtractAlpha(fv)
				case "n":
					fv, _ = ExtractNumeric(fv)
				case "an":
					fv, _ = ExtractAlphaNumeric(fv)
				case "ans":
					if !hasGetter {
						fv, _ = ExtractAlphaNumericPrintableSymbols(fv)
					}
				case "b":
					if len(boolTrue) == 0 && len(boolFalse) == 0 {
						if StringSliceContains(&trueList, strings.ToLower(fv)) {
							fv = "true"
						} else {
							fv = "false"
						}
					} else {
						if Trim(boolTrue) == Trim(boolFalse) {
							if fv == "false" {
								fv = ""
								csvList[tagPos] = fv
								continue
							}
						}
					}
				case "regex":
					fv, _ = ExtractByRegex(fv, tagRegEx)
				case "h":
					fv, _ = ExtractHex(fv)
				case "b64":
					fv, _ = ExtractAlphaNumericPrintableSymbols(fv)
				}

				if boolFalse == " " && origFv == "false" && len(outPrefix) > 0 {
					// just in case fv is not defined type type b
					fv = ""
					csvList[tagPos] = fv
					continue
				}

				if len(fv) == 0 && len(defVal) > 0 {
					fv = defVal
				}

				if tagType == "a" || tagType == "an" || tagType == "ans" || tagType == "n" || tagType == "regex" || tagType == "h" || tagType == "b64" {
					if sizeMin > 0 && len(fv) > 0 {
						if len(fv) < sizeMin {
							return "", fmt.Errorf("%s Min Length is %d", field.Name, sizeMin)
						}
					}

					if sizeMax > 0 && len(fv) > sizeMax {
						fv = Left(fv, sizeMax)
					}

					if tagModulo > 0 {
						if len(fv)%tagModulo != 0 {
							return "", fmt.Errorf("Struct Field %s Expects Value In Blocks of %d Characters", field.Name, tagModulo)
						}
					}
				}

				if tagType == "n" {
					n, ok := ParseInt32(fv)

					if ok {
						if rangeMin > 0 {
							if n < rangeMin {
								if !(n == 0 && tagReq != "true") {
									return "", fmt.Errorf("%s Range Minimum is %d", field.Name, rangeMin)
								}
							}
						}

						if rangeMax > 0 {
							if n > rangeMax {
								return "", fmt.Errorf("%s Range Maximum is %d", field.Name, rangeMax)
							}
						}
					}
				}

				if tagReq == "true" && len(fv) == 0 {
					return "", fmt.Errorf("%s is a Required Field", field.Name)
				}
			}

			// validate if applicable
			if valData := Trim(field.Tag.Get("validate")); len(valData) >= 3 {
				valComp := Left(valData, 2)
				valData = Right(valData, len(valData)-2)

				switch valComp {
				case "==":
					valAr := strings.Split(valData, "||")

					if len(valAr) <= 1 {
						if strings.ToLower(fv) != strings.ToLower(valData) {
							if len(fv) > 0 || tagReq == "true" {
								return "", fmt.Errorf("%s Validation Failed: Expected To Match '%s', But Received '%s'", field.Name, valData, fv)
							}
						}
					} else {
						found := false

						for _, va := range valAr {
							if strings.ToLower(fv) == strings.ToLower(va) {
								found = true
								break
							}
						}

						if !found && (len(fv) > 0 || tagReq == "true") {
							return "", fmt.Errorf("%s Validation Failed: Expected To Match '%s', But Received '%s'", field.Name, strings.ReplaceAll(valData, "||", " or "), fv)
						}
					}
				case "!=":
					valAr := strings.Split(valData, "&&")

					if len(valAr) <= 1 {
						if strings.ToLower(fv) == strings.ToLower(valData) {
							if len(fv) > 0 || tagReq == "true" {
								return "", fmt.Errorf("%s Validation Failed: Expected To Not Match '%s', But Received '%s'", field.Name, valData, fv)
							}
						}
					} else {
						found := false

						for _, va := range valAr {
							if strings.ToLower(fv) == strings.ToLower(va) {
								found = true
								break
							}
						}

						if found && (len(fv) > 0 || tagReq == "true") {
							return "", fmt.Errorf("%s Validation Failed: Expected To Not Match '%s', But Received '%s'", field.Name, strings.ReplaceAll(valData, "&&", " and "), fv)
						}
					}
				case "<=":
					if valNum, valOk := ParseFloat64(valData); valOk {
						if srcNum, _ := ParseFloat64(fv); srcNum > valNum {
							if len(fv) > 0 || tagReq == "true" {
								return "", fmt.Errorf("%s Validation Failed: Expected To Be Less or Equal To '%s', But Received '%s'", field.Name, valData, fv)
							}
						}
					}
				case "<<":
					if valNum, valOk := ParseFloat64(valData); valOk {
						if srcNum, _ := ParseFloat64(fv); srcNum >= valNum {
							if len(fv) > 0 || tagReq == "true" {
								return "", fmt.Errorf("%s Validation Failed: Expected To Be Less Than '%s', But Received '%s'", field.Name, valData, fv)
							}
						}
					}
				case ">=":
					if valNum, valOk := ParseFloat64(valData); valOk {
						if srcNum, _ := ParseFloat64(fv); srcNum < valNum {
							if len(fv) > 0 || tagReq == "true" {
								return "", fmt.Errorf("%s Validation Failed: Expected To Be Greater or Equal To '%s', But Received '%s'", field.Name, valData, fv)
							}
						}
					}
				case ">>":
					if valNum, valOk := ParseFloat64(valData); valOk {
						if srcNum, _ := ParseFloat64(fv); srcNum <= valNum {
							if len(fv) > 0 || tagReq == "true" {
								return "", fmt.Errorf("%s Validation Failed: Expected To Be Greater Than '%s', But Received '%s'", field.Name, valData, fv)
							}
						}
					}
				case ":=":
					if len(valData) > 0 {
						if retV, nf := ReflectCall(s.Addr(), valData); !nf {
							if len(retV) > 0 {
								if retV[0].Kind() == reflect.Bool && !retV[0].Bool() {
									// validation failed with bool false
									return "", fmt.Errorf("%s Validation Failed: %s() Returned Result is False", field.Name, valData)
								} else if retErr := DerefError(retV[0]); retErr != nil {
									// validation failed with error
									return "", fmt.Errorf("%s Validation On %s() Failed: %s", field.Name, valData, retErr.Error())
								}
							}
						}
					}
				}
			}

			// store fv into sorted slice
			if skipBlank && LenTrim(fv) == 0 {
				csvList[tagPos] = ""
			} else if skipZero && fv == "0" {
				csvList[tagPos] = ""
			} else {
				csvList[tagPos] = outPrefix + fv
			}
		}
	}

	for _, v := range csvList {
		if v != "{?}" {
			if LenTrim(csvPayload) > 0 {
				csvPayload += csvDelimiter
			}

			csvPayload += v
		}
	}

	return csvPayload, nil
}


