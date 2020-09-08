package httprange

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testData    = "1234567890abcdefghij0987654321"
	testDataLen = len(testData)
)

func TestSectionReader(t *testing.T) {
	tests := map[string]struct {
		sectionOffset   int
		sectionSize     int
		readSize        int
		expectedContent string
		expectedErr     error
	}{
		"no_buffer_no_err": {
			sectionOffset:   0,
			sectionSize:     testDataLen,
			readSize:        0,
			expectedContent: "",
			expectedErr:     nil,
		},
		"offset_starts_at_size": {
			sectionOffset:   testDataLen,
			sectionSize:     1,
			readSize:        1,
			expectedContent: "",
			expectedErr:     ErrInvalidRange,
		},
		"read_all": {
			sectionOffset:   0,
			sectionSize:     testDataLen,
			readSize:        testDataLen,
			expectedContent: testData,
			expectedErr:     io.EOF,
		},
		"read_first_half": {
			sectionOffset:   0,
			sectionSize:     testDataLen / 2,
			readSize:        testDataLen / 2,
			expectedContent: testData[:testDataLen/2],
			expectedErr:     io.EOF,
		},
		"read_second_half": {
			sectionOffset:   testDataLen / 2,
			sectionSize:     testDataLen / 2,
			readSize:        testDataLen / 2,
			expectedContent: testData[testDataLen/2:],
			expectedErr:     io.EOF,
		},
		"read_15_bytes_with_offset": {
			sectionOffset:   3,
			sectionSize:     testDataLen / 2,
			readSize:        testDataLen / 2,
			expectedContent: testData[3 : 3+testDataLen/2],
			expectedErr:     io.EOF,
		},
		"read_13_bytes_with_offset": {
			sectionOffset:   10,
			sectionSize:     testDataLen/2 - 2,
			readSize:        testDataLen/2 - 2,
			expectedContent: testData[10 : 10+testDataLen/2-2],
			expectedErr:     io.EOF,
		},
	}

	testServer := newTestServer(t, nil)
	defer testServer.Close()

	resource, err := NewResource(context.Background(), testServer.URL+"/resource")
	require.NoError(t, err)

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rr := NewRangedReader(resource)
			s := rr.SectionReader(int64(tt.sectionOffset), int64(tt.sectionSize))
			defer s.Close()

			buf := make([]byte, tt.readSize)
			n, err := s.Read(buf)
			if tt.expectedErr != nil && err != io.EOF {
				require.EqualError(t, err, tt.expectedErr.Error())
				return
			}

			require.Equal(t, tt.expectedErr, err)
			require.Equal(t, len(tt.expectedContent), n)
			require.Equal(t, tt.expectedContent, string(buf[:n]))
		})
	}
}

func TestReadAt(t *testing.T) {
	tests := map[string]struct {
		sectionOffset   int
		readSize        int
		expectedContent string
		expectedErr     error
	}{
		"no_buffer_no_err": {
			sectionOffset:   0,
			readSize:        0,
			expectedContent: "",
			expectedErr:     nil,
		},
		"offset_starts_at_size": {
			sectionOffset:   testDataLen,
			readSize:        1,
			expectedContent: "",
			expectedErr:     ErrInvalidRange,
		},
		"read_at_end": {
			sectionOffset:   testDataLen,
			readSize:        1,
			expectedContent: "",
			expectedErr:     ErrInvalidRange,
		},
		"read_all": {
			sectionOffset:   0,
			readSize:        testDataLen,
			expectedContent: testData,
			expectedErr:     nil,
		},
		"read_first_half": {
			sectionOffset:   0,
			readSize:        testDataLen / 2,
			expectedContent: testData[:testDataLen/2],
			expectedErr:     nil,
		},
		"read_second_half": {
			sectionOffset:   testDataLen / 2,
			readSize:        testDataLen / 2,
			expectedContent: testData[testDataLen/2:],
			expectedErr:     nil,
		},
		"read_15_bytes_with_offset": {
			sectionOffset:   3,
			readSize:        testDataLen / 2,
			expectedContent: testData[3 : 3+testDataLen/2],
			expectedErr:     nil,
		},
		"read_13_bytes_with_offset": {
			sectionOffset:   10,
			readSize:        testDataLen/2 - 2,
			expectedContent: testData[10 : 10+testDataLen/2-2],
			expectedErr:     nil,
		},
	}

	testServer := newTestServer(t, nil)
	defer testServer.Close()

	resource, err := NewResource(context.Background(), testServer.URL+"/resource")
	require.NoError(t, err)

	for name, tt := range tests {
		rr := NewRangedReader(resource)
		testFn := func(reader *RangedReader) func(t *testing.T) {
			return func(t *testing.T) {
				buf := make([]byte, tt.readSize)

				n, err := reader.ReadAt(buf, int64(tt.sectionOffset))
				if tt.expectedErr != nil {
					require.EqualError(t, err, tt.expectedErr.Error())
					return
				}

				require.NoError(t, err)
				require.Equal(t, len(tt.expectedContent), n)
				require.Equal(t, tt.expectedContent, string(buf[:n]))
			}
		}

		t.Run(name, func(t *testing.T) {
			rr.WithCachedReader(func() {
				t.Run("cachedReader", testFn(rr))
			})

			t.Run("ephemeralReader", testFn(rr))
		})
	}
}

func TestReadAtMultipart(t *testing.T) {
	var counter int32

	testServer := newTestServer(t, func() {
		atomic.AddInt32(&counter, 1)
	})
	defer testServer.Close()

	resource, err := NewResource(context.Background(), testServer.URL+"/resource")
	require.NoError(t, err)
	require.Equal(t, int32(1), counter)

	rr := NewRangedReader(resource)

	assertReadAtFunc := func(t *testing.T, bufLen, offset int, expectedDat string, expectedCounter int32) {
		buf := make([]byte, bufLen)
		n, err := rr.ReadAt(buf, int64(offset))
		require.NoError(t, err)
		require.Equal(t, expectedCounter, counter)

		require.NoError(t, err)
		require.Equal(t, bufLen, n)
		require.Equal(t, expectedDat, string(buf))
	}
	bufLen := testDataLen / 3

	t.Run("ephemeralRead", func(t *testing.T) {
		// "1234567890"
		assertReadAtFunc(t, bufLen, 0, testData[:bufLen], 2)
		// "abcdefghij"
		assertReadAtFunc(t, bufLen, bufLen, testData[bufLen:2*bufLen], 3)
		// "0987654321"
		assertReadAtFunc(t, bufLen, 2*bufLen, testData[2*bufLen:], 4)
	})

	// cachedReader should not make extra requests, the expectedCounter should always be the same
	counter = 1
	t.Run("cachedReader", func(t *testing.T) {
		rr.WithCachedReader(func() {
			// "1234567890"
			assertReadAtFunc(t, bufLen, 0, testData[:bufLen], 2)
			// "abcdefghij"
			assertReadAtFunc(t, bufLen, bufLen, testData[bufLen:2*bufLen], 2)
			// "0987654321"
			assertReadAtFunc(t, bufLen, 2*bufLen, testData[2*bufLen:], 2)
		})
	})
}

func newTestServer(t *testing.T, do func()) *httptest.Server {
	t.Helper()

	// use a constant known time or else http.ServeContent will change Last-Modified value
	tNow, err := time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")
	require.NoError(t, err)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if do != nil {
			do()
		}

		http.ServeContent(w, r, r.URL.Path, tNow, strings.NewReader(testData))
	}))
}