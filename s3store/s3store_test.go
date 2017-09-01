package s3store_test

import (
	"bytes"
	"fmt"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/tus/tusd"
	"github.com/tus/tusd/s3store"
)

//go:generate mockgen -destination=./s3store_mock_test.go -package=s3store_test github.com/tus/tusd/s3store S3API

// Test interface implementations
var _ tusd.DataStore = s3store.S3Store{}
var _ tusd.GetReaderDataStore = s3store.S3Store{}
var _ tusd.TerminaterDataStore = s3store.S3Store{}
var _ tusd.FinisherDataStore = s3store.S3Store{}
var _ tusd.ConcaterDataStore = s3store.S3Store{}

func TestCalcOptimalPartSize(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	// If you quickly want to override the default values in this test
	/*
		store.MinPartSize = 2
		store.MaxPartSize = 10
		store.MaxMultipartParts = 20
		store.MaxObjectSize = 200
	*/

	// If you want the results of all tests printed
	debug := false

	// sanity check
	if store.MaxObjectSize > store.MaxPartSize*store.MaxMultipartParts {
		t.Errorf("MaxObjectSize %v can never be achieved, as MaxMultipartParts %v and MaxPartSize %v only allow for an upload of %v bytes total.\n", store.MaxObjectSize, store.MaxMultipartParts, store.MaxPartSize, store.MaxMultipartParts*store.MaxPartSize)
	}

	HighestApplicablePartSize := store.MaxObjectSize / store.MaxMultipartParts
	if store.MaxObjectSize%store.MaxMultipartParts > 0 {
		HighestApplicablePartSize++
	}
	RemainderWithHighestApplicablePartSize := store.MaxObjectSize % HighestApplicablePartSize

	// some of these tests are actually duplicates, as they specify the same size
	// in bytes - two ways to describe the same thing. That is wanted, in order
	// to provide a full picture from any angle.
	testcases := []int64{
		0,
		1,
		store.MinPartSize - 1,
		store.MinPartSize,
		store.MinPartSize + 1,

		store.MinPartSize*(store.MaxMultipartParts-1) - 1,
		store.MinPartSize * (store.MaxMultipartParts - 1),
		store.MinPartSize*(store.MaxMultipartParts-1) + 1,

		store.MinPartSize*store.MaxMultipartParts - 1,
		store.MinPartSize * store.MaxMultipartParts,
		store.MinPartSize*store.MaxMultipartParts + 1,

		store.MinPartSize*(store.MaxMultipartParts+1) - 1,
		store.MinPartSize * (store.MaxMultipartParts + 1),
		store.MinPartSize*(store.MaxMultipartParts+1) + 1,

		(HighestApplicablePartSize-1)*store.MaxMultipartParts - 1,
		(HighestApplicablePartSize - 1) * store.MaxMultipartParts,
		(HighestApplicablePartSize-1)*store.MaxMultipartParts + 1,

		HighestApplicablePartSize*(store.MaxMultipartParts-1) - 1,
		HighestApplicablePartSize * (store.MaxMultipartParts - 1),
		HighestApplicablePartSize*(store.MaxMultipartParts-1) + 1,

		HighestApplicablePartSize*(store.MaxMultipartParts-1) + RemainderWithHighestApplicablePartSize - 1,
		HighestApplicablePartSize*(store.MaxMultipartParts-1) + RemainderWithHighestApplicablePartSize,
		HighestApplicablePartSize*(store.MaxMultipartParts-1) + RemainderWithHighestApplicablePartSize + 1,

		store.MaxObjectSize - 1,
		store.MaxObjectSize,
		store.MaxObjectSize + 1,

		(store.MaxObjectSize/store.MaxMultipartParts)*(store.MaxMultipartParts-1) - 1,
		(store.MaxObjectSize / store.MaxMultipartParts) * (store.MaxMultipartParts - 1),
		(store.MaxObjectSize/store.MaxMultipartParts)*(store.MaxMultipartParts-1) + 1,

		store.MaxPartSize*(store.MaxMultipartParts-1) - 1,
		store.MaxPartSize * (store.MaxMultipartParts - 1),
		store.MaxPartSize*(store.MaxMultipartParts-1) + 1,

		store.MaxPartSize*store.MaxMultipartParts - 1,
		store.MaxPartSize * store.MaxMultipartParts,
		// We cannot calculate a part size for store.MaxPartSize*store.MaxMultipartParts + 1
		// This case is tested in TestCalcOptimalPartSize_ExceedingMaxPartSize
	}

	for _, size := range testcases {
		optimalPartSize, calcError := store.CalcOptimalPartSize(size)
		assert.Nil(calcError, "Size %d, no error should be returned.\n", size)

		// Number of parts with the same size
		equalparts := size / optimalPartSize
		// Size of the last part (or 0 if no spare part is needed)
		lastpartSize := size % optimalPartSize

		prelude := fmt.Sprintf("Size %d, %d parts of size %d, lastpart %d: ", size, equalparts, optimalPartSize, lastpartSize)

		assert.False(optimalPartSize < store.MinPartSize, prelude+"optimalPartSize < MinPartSize %d.\n", store.MinPartSize)
		assert.False(optimalPartSize > store.MaxPartSize, prelude+"optimalPartSize > MaxPartSize %d.\n", store.MaxPartSize)
		assert.False(lastpartSize == 0 && equalparts > store.MaxMultipartParts, prelude+"more parts than MaxMultipartParts %d.\n", store.MaxMultipartParts)
		assert.False(lastpartSize > 0 && equalparts > store.MaxMultipartParts-1, prelude+"more parts than MaxMultipartParts %d.\n", store.MaxMultipartParts)
		assert.False(lastpartSize > store.MaxPartSize, prelude+"lastpart > MaxPartSize %d.\n", store.MaxPartSize)
		assert.False(lastpartSize > optimalPartSize, prelude+"lastpart > optimalPartSize %d.\n", optimalPartSize)
		assert.True(optimalPartSize*store.MaxMultipartParts < size, prelude+"upload does not fit in %d parts.\n", store.MaxMultipartParts)

		if debug {
			fmt.Printf(prelude+"does exceed MaxObjectSize: %t.\n", size > store.MaxObjectSize)
		}
	}
	if debug {
	  fmt.Println("HighestApplicablePartSize", HighestApplicablePartSize)
	  fmt.Println("RemainderWithHighestApplicablePartSize", RemainderWithHighestApplicablePartSize)
	}
}

func TestCalcOptimalPartSize_ExceedingMaxPartSize(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	size := store.MaxPartSize*store.MaxMultipartParts + 1

	_, err := store.CalcOptimalPartSize(size)
	assert.Error(err, "CalcOptimalPartSize: to upload %v bytes optimalPartSize %v must exceed MaxPartSize %v")
}

func TestAllPartSizes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	store.MinPartSize = 5
	store.MaxPartSize = 5 * 1024
	store.MaxMultipartParts = 1000
	store.MaxObjectSize = store.MaxPartSize * store.MaxMultipartParts

	// If you want the results of all tests printed
	debug := false

	// sanity check
	if store.MaxObjectSize > store.MaxPartSize*store.MaxMultipartParts {
		t.Errorf("MaxObjectSize %v can never be achieved, as MaxMultipartParts %v and MaxPartSize %v only allow for an upload of %v bytes total.\n", store.MaxObjectSize, store.MaxMultipartParts, store.MaxPartSize, store.MaxMultipartParts*store.MaxPartSize)
	}

	for size := int64(0); size <= store.MaxObjectSize; size++ {
		optimalPartSize, calcError := store.CalcOptimalPartSize(size)
		assert.Nil(calcError, "Size %d, no error should be returned.\n", size)

		// Number of parts with the same size
		equalparts := size / optimalPartSize
		// Size of the last part (or 0 if no spare part is needed)
		lastpartSize := size % optimalPartSize

		prelude := fmt.Sprintf("Size %d, %d parts of size %d, lastpart %d: ", size, equalparts, optimalPartSize, lastpartSize)

		assert.False(optimalPartSize < store.MinPartSize, prelude+"optimalPartSize < MinPartSize %d.\n", store.MinPartSize)
		assert.False(optimalPartSize > store.MaxPartSize, prelude+"optimalPartSize > MaxPartSize %d.\n", store.MaxPartSize)
		assert.False(lastpartSize == 0 && equalparts > store.MaxMultipartParts, prelude+"more parts than MaxMultipartParts %d.\n", store.MaxMultipartParts)
		assert.False(lastpartSize > 0 && equalparts > store.MaxMultipartParts-1, prelude+"more parts than MaxMultipartParts %d.\n", store.MaxMultipartParts)
		assert.False(lastpartSize > store.MaxPartSize, prelude+"lastpart > MaxPartSize %d.\n", store.MaxPartSize)
		assert.False(lastpartSize > optimalPartSize, prelude+"lastpart > optimalPartSize %d.\n", optimalPartSize)
		assert.True(optimalPartSize*store.MaxMultipartParts < size, prelude+"upload does not fit in %d parts.\n", store.MaxMultipartParts)
		if debug {
			fmt.Printf("Size %v, %v parts of size %v, lastpart %v, does exceed MaxObjectSize %v.\n", size, equalparts, optimalPartSize, lastpartsize, size > store.MaxObjectSize)
		}
	}
}

func TestNewUpload(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	assert.Equal("bucket", store.Bucket)
	assert.Equal(s3obj, store.Service)

	s1 := "hello"
	s2 := "men?"

	gomock.InOrder(
		s3obj.EXPECT().CreateMultipartUpload(&s3.CreateMultipartUploadInput{
			Bucket: aws.String("bucket"),
			Key:    aws.String("uploadId"),
			Metadata: map[string]*string{
				"foo": &s1,
				"bar": &s2,
			},
		}).Return(&s3.CreateMultipartUploadOutput{
			UploadId: aws.String("multipartId"),
		}, nil),
		s3obj.EXPECT().PutObject(&s3.PutObjectInput{
			Bucket:        aws.String("bucket"),
			Key:           aws.String("uploadId.info"),
			Body:          bytes.NewReader([]byte(`{"ID":"uploadId+multipartId","Size":500,"Offset":0,"MetaData":{"bar":"menü","foo":"hello"},"IsPartial":false,"IsFinal":false,"PartialUploads":null}`)),
			ContentLength: aws.Int64(int64(148)),
		}),
	)

	info := tusd.FileInfo{
		ID:   "uploadId",
		Size: 500,
		MetaData: map[string]string{
			"foo": "hello",
			"bar": "menü",
		},
	}

	id, err := store.NewUpload(info)
	assert.Nil(err)
	assert.Equal("uploadId+multipartId", id)
}

func TestNewUploadLargerMaxObjectSize(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	assert.Equal("bucket", store.Bucket)
	assert.Equal(s3obj, store.Service)

	info := tusd.FileInfo{
		ID:   "uploadId",
		Size: store.MaxObjectSize + 1,
	}

	id, err := store.NewUpload(info)
	assert.NotNil(err)
	assert.EqualError(err, fmt.Sprintf("s3store: upload size of %v bytes exceeds MaxObjectSize of %v bytes", info.Size, store.MaxObjectSize))
	assert.Equal("", id)
}

func TestGetInfoNotFound(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	s3obj.EXPECT().GetObject(&s3.GetObjectInput{
		Bucket: aws.String("bucket"),
		Key:    aws.String("uploadId.info"),
	}).Return(nil, awserr.New("NoSuchKey", "The specified key does not exist.", nil))

	_, err := store.GetInfo("uploadId+multipartId")
	assert.Equal(tusd.ErrNotFound, err)
}

func TestGetInfo(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	gomock.InOrder(
		s3obj.EXPECT().GetObject(&s3.GetObjectInput{
			Bucket: aws.String("bucket"),
			Key:    aws.String("uploadId.info"),
		}).Return(&s3.GetObjectOutput{
			Body: ioutil.NopCloser(bytes.NewReader([]byte(`{"ID":"uploadId+multipartId","Size":500,"Offset":0,"MetaData":{"bar":"menü","foo":"hello"},"IsPartial":false,"IsFinal":false,"PartialUploads":null}`))),
		}, nil),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(0),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					Size: aws.Int64(100),
				},
				{
					Size: aws.Int64(200),
				},
			},
			NextPartNumberMarker: aws.Int64(2),
			IsTruncated:          aws.Bool(true),
		}, nil),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(2),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					Size: aws.Int64(100),
				},
			},
		}, nil),
	)

	info, err := store.GetInfo("uploadId+multipartId")
	assert.Nil(err)
	assert.Equal(int64(500), info.Size)
	assert.Equal(int64(400), info.Offset)
	assert.Equal("uploadId+multipartId", info.ID)
	assert.Equal("hello", info.MetaData["foo"])
	assert.Equal("menü", info.MetaData["bar"])
}

func TestGetInfoFinished(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	gomock.InOrder(
		s3obj.EXPECT().GetObject(&s3.GetObjectInput{
			Bucket: aws.String("bucket"),
			Key:    aws.String("uploadId.info"),
		}).Return(&s3.GetObjectOutput{
			Body: ioutil.NopCloser(bytes.NewReader([]byte(`{"ID":"uploadId","Size":500,"Offset":0,"MetaData":null,"IsPartial":false,"IsFinal":false,"PartialUploads":null}`))),
		}, nil),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(0),
		}).Return(nil, awserr.New("NoSuchUpload", "The specified upload does not exist.", nil)),
	)

	info, err := store.GetInfo("uploadId+multipartId")
	assert.Nil(err)
	assert.Equal(int64(500), info.Size)
	assert.Equal(int64(500), info.Offset)
}

func TestGetReader(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	s3obj.EXPECT().GetObject(&s3.GetObjectInput{
		Bucket: aws.String("bucket"),
		Key:    aws.String("uploadId"),
	}).Return(&s3.GetObjectOutput{
		Body: ioutil.NopCloser(bytes.NewReader([]byte(`hello world`))),
	}, nil)

	content, err := store.GetReader("uploadId+multipartId")
	assert.Nil(err)
	assert.Equal(ioutil.NopCloser(bytes.NewReader([]byte(`hello world`))), content)
}

func TestGetReaderNotFound(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	gomock.InOrder(
		s3obj.EXPECT().GetObject(&s3.GetObjectInput{
			Bucket: aws.String("bucket"),
			Key:    aws.String("uploadId"),
		}).Return(nil, awserr.New("NoSuchKey", "The specified key does not exist.", nil)),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:   aws.String("bucket"),
			Key:      aws.String("uploadId"),
			UploadId: aws.String("multipartId"),
			MaxParts: aws.Int64(0),
		}).Return(nil, awserr.New("NoSuchUpload", "The specified upload does not exist.", nil)),
	)

	content, err := store.GetReader("uploadId+multipartId")
	assert.Nil(content)
	assert.Equal(tusd.ErrNotFound, err)
}

func TestGetReaderNotFinished(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	gomock.InOrder(
		s3obj.EXPECT().GetObject(&s3.GetObjectInput{
			Bucket: aws.String("bucket"),
			Key:    aws.String("uploadId"),
		}).Return(nil, awserr.New("NoSuchKey", "The specified key does not exist.", nil)),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:   aws.String("bucket"),
			Key:      aws.String("uploadId"),
			UploadId: aws.String("multipartId"),
			MaxParts: aws.Int64(0),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{},
		}, nil),
	)

	content, err := store.GetReader("uploadId+multipartId")
	assert.Nil(content)
	assert.Equal("cannot stream non-finished upload", err.Error())
}

func TestFinishUpload(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	gomock.InOrder(
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(0),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					Size:       aws.Int64(100),
					ETag:       aws.String("foo"),
					PartNumber: aws.Int64(1),
				},
				{
					Size:       aws.Int64(200),
					ETag:       aws.String("bar"),
					PartNumber: aws.Int64(2),
				},
			},
			NextPartNumberMarker: aws.Int64(2),
			IsTruncated:          aws.Bool(true),
		}, nil),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(2),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					Size:       aws.Int64(100),
					ETag:       aws.String("foobar"),
					PartNumber: aws.Int64(3),
				},
			},
		}, nil),
		s3obj.EXPECT().CompleteMultipartUpload(&s3.CompleteMultipartUploadInput{
			Bucket:   aws.String("bucket"),
			Key:      aws.String("uploadId"),
			UploadId: aws.String("multipartId"),
			MultipartUpload: &s3.CompletedMultipartUpload{
				Parts: []*s3.CompletedPart{
					{
						ETag:       aws.String("foo"),
						PartNumber: aws.Int64(1),
					},
					{
						ETag:       aws.String("bar"),
						PartNumber: aws.Int64(2),
					},
					{
						ETag:       aws.String("foobar"),
						PartNumber: aws.Int64(3),
					},
				},
			},
		}).Return(nil, nil),
	)

	err := store.FinishUpload("uploadId+multipartId")
	assert.Nil(err)
}

func TestWriteChunk(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)
	store.MaxPartSize = 8
	store.MinPartSize = 4
	store.MaxMultipartParts = 10000
	store.MaxObjectSize = 5 * 1024 * 1024 * 1024 * 1024

	gomock.InOrder(
		s3obj.EXPECT().GetObject(&s3.GetObjectInput{
			Bucket: aws.String("bucket"),
			Key:    aws.String("uploadId.info"),
		}).Return(&s3.GetObjectOutput{
			Body: ioutil.NopCloser(bytes.NewReader([]byte(`{"ID":"uploadId","Size":500,"Offset":0,"MetaData":null,"IsPartial":false,"IsFinal":false,"PartialUploads":null}`))),
		}, nil),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(0),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					Size: aws.Int64(100),
				},
				{
					Size: aws.Int64(200),
				},
			},
		}, nil),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(0),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					Size: aws.Int64(100),
				},
				{
					Size: aws.Int64(200),
				},
			},
		}, nil),
		s3obj.EXPECT().UploadPart(NewUploadPartInputMatcher(&s3.UploadPartInput{
			Bucket:     aws.String("bucket"),
			Key:        aws.String("uploadId"),
			UploadId:   aws.String("multipartId"),
			PartNumber: aws.Int64(3),
			Body:       bytes.NewReader([]byte("1234")),
		})).Return(nil, nil),
		s3obj.EXPECT().UploadPart(NewUploadPartInputMatcher(&s3.UploadPartInput{
			Bucket:     aws.String("bucket"),
			Key:        aws.String("uploadId"),
			UploadId:   aws.String("multipartId"),
			PartNumber: aws.Int64(4),
			Body:       bytes.NewReader([]byte("5678")),
		})).Return(nil, nil),
		s3obj.EXPECT().UploadPart(NewUploadPartInputMatcher(&s3.UploadPartInput{
			Bucket:     aws.String("bucket"),
			Key:        aws.String("uploadId"),
			UploadId:   aws.String("multipartId"),
			PartNumber: aws.Int64(5),
			Body:       bytes.NewReader([]byte("90AB")),
		})).Return(nil, nil),
	)

	// The last bytes "CD" will be ignored, as they are not the last bytes of the
	// upload (500 bytes total) and not of full part-size.
	bytesRead, err := store.WriteChunk("uploadId+multipartId", 300, bytes.NewReader([]byte("1234567890ABCD")))
	assert.Nil(err)
	assert.Equal(int64(12), bytesRead)
}

func TestWriteChunkDropTooSmall(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	gomock.InOrder(
		s3obj.EXPECT().GetObject(&s3.GetObjectInput{
			Bucket: aws.String("bucket"),
			Key:    aws.String("uploadId.info"),
		}).Return(&s3.GetObjectOutput{
			Body: ioutil.NopCloser(bytes.NewReader([]byte(`{"ID":"uploadId","Size":500,"Offset":0,"MetaData":null,"IsPartial":false,"IsFinal":false,"PartialUploads":null}`))),
		}, nil),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(0),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					Size: aws.Int64(100),
				},
				{
					Size: aws.Int64(200),
				},
			},
		}, nil),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(0),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					Size: aws.Int64(100),
				},
				{
					Size: aws.Int64(200),
				},
			},
		}, nil),
	)

	bytesRead, err := store.WriteChunk("uploadId+multipartId", 300, bytes.NewReader([]byte("1234567890")))
	assert.Nil(err)
	assert.Equal(int64(0), bytesRead)
}

func TestWriteChunkAllowTooSmallLast(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)
	store.MinPartSize = 20

	gomock.InOrder(
		s3obj.EXPECT().GetObject(&s3.GetObjectInput{
			Bucket: aws.String("bucket"),
			Key:    aws.String("uploadId.info"),
		}).Return(&s3.GetObjectOutput{
			Body: ioutil.NopCloser(bytes.NewReader([]byte(`{"ID":"uploadId","Size":500,"Offset":0,"MetaData":null,"IsPartial":false,"IsFinal":false,"PartialUploads":null}`))),
		}, nil),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(0),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					Size: aws.Int64(400),
				},
				{
					Size: aws.Int64(90),
				},
			},
		}, nil),
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(0),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					Size: aws.Int64(400),
				},
				{
					Size: aws.Int64(90),
				},
			},
		}, nil),
		s3obj.EXPECT().UploadPart(NewUploadPartInputMatcher(&s3.UploadPartInput{
			Bucket:     aws.String("bucket"),
			Key:        aws.String("uploadId"),
			UploadId:   aws.String("multipartId"),
			PartNumber: aws.Int64(3),
			Body:       bytes.NewReader([]byte("1234567890")),
		})).Return(nil, nil),
	)

	// 10 bytes are missing for the upload to be finished (offset at 490 for 500
	// bytes file) but the minimum chunk size is higher (20). The chunk is
	// still uploaded since the last part may be smaller than the minimum.
	bytesRead, err := store.WriteChunk("uploadId+multipartId", 490, bytes.NewReader([]byte("1234567890")))
	assert.Nil(err)
	assert.Equal(int64(10), bytesRead)
}

func TestTerminate(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	// Order is not important in this situation.
	s3obj.EXPECT().AbortMultipartUpload(&s3.AbortMultipartUploadInput{
		Bucket:   aws.String("bucket"),
		Key:      aws.String("uploadId"),
		UploadId: aws.String("multipartId"),
	}).Return(nil, nil)

	s3obj.EXPECT().DeleteObjects(&s3.DeleteObjectsInput{
		Bucket: aws.String("bucket"),
		Delete: &s3.Delete{
			Objects: []*s3.ObjectIdentifier{
				{
					Key: aws.String("uploadId"),
				},
				{
					Key: aws.String("uploadId.info"),
				},
			},
			Quiet: aws.Bool(true),
		},
	}).Return(&s3.DeleteObjectsOutput{}, nil)

	err := store.Terminate("uploadId+multipartId")
	assert.Nil(err)
}

func TestTerminateWithErrors(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	// Order is not important in this situation.
	// NoSuchUpload errors should be ignored
	s3obj.EXPECT().AbortMultipartUpload(&s3.AbortMultipartUploadInput{
		Bucket:   aws.String("bucket"),
		Key:      aws.String("uploadId"),
		UploadId: aws.String("multipartId"),
	}).Return(nil, awserr.New("NoSuchUpload", "The specified upload does not exist.", nil))

	s3obj.EXPECT().DeleteObjects(&s3.DeleteObjectsInput{
		Bucket: aws.String("bucket"),
		Delete: &s3.Delete{
			Objects: []*s3.ObjectIdentifier{
				{
					Key: aws.String("uploadId"),
				},
				{
					Key: aws.String("uploadId.info"),
				},
			},
			Quiet: aws.Bool(true),
		},
	}).Return(&s3.DeleteObjectsOutput{
		Errors: []*s3.Error{
			{
				Code:    aws.String("hello"),
				Key:     aws.String("uploadId"),
				Message: aws.String("it's me."),
			},
		},
	}, nil)

	err := store.Terminate("uploadId+multipartId")
	assert.Equal("Multiple errors occurred:\n\tAWS S3 Error (hello) for object uploadId: it's me.\n", err.Error())
}

func TestConcatUploads(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	s3obj := NewMockS3API(mockCtrl)
	store := s3store.New("bucket", s3obj)

	s3obj.EXPECT().UploadPartCopy(&s3.UploadPartCopyInput{
		Bucket:     aws.String("bucket"),
		Key:        aws.String("uploadId"),
		UploadId:   aws.String("multipartId"),
		CopySource: aws.String("bucket/aaa"),
		PartNumber: aws.Int64(1),
	}).Return(nil, nil)

	s3obj.EXPECT().UploadPartCopy(&s3.UploadPartCopyInput{
		Bucket:     aws.String("bucket"),
		Key:        aws.String("uploadId"),
		UploadId:   aws.String("multipartId"),
		CopySource: aws.String("bucket/bbb"),
		PartNumber: aws.Int64(2),
	}).Return(nil, nil)

	s3obj.EXPECT().UploadPartCopy(&s3.UploadPartCopyInput{
		Bucket:     aws.String("bucket"),
		Key:        aws.String("uploadId"),
		UploadId:   aws.String("multipartId"),
		CopySource: aws.String("bucket/ccc"),
		PartNumber: aws.Int64(3),
	}).Return(nil, nil)

	// Output from s3Store.FinishUpload
	gomock.InOrder(
		s3obj.EXPECT().ListParts(&s3.ListPartsInput{
			Bucket:           aws.String("bucket"),
			Key:              aws.String("uploadId"),
			UploadId:         aws.String("multipartId"),
			PartNumberMarker: aws.Int64(0),
		}).Return(&s3.ListPartsOutput{
			Parts: []*s3.Part{
				{
					ETag:       aws.String("foo"),
					PartNumber: aws.Int64(1),
				},
				{
					ETag:       aws.String("bar"),
					PartNumber: aws.Int64(2),
				},
				{
					ETag:       aws.String("baz"),
					PartNumber: aws.Int64(3),
				},
			},
		}, nil),
		s3obj.EXPECT().CompleteMultipartUpload(&s3.CompleteMultipartUploadInput{
			Bucket:   aws.String("bucket"),
			Key:      aws.String("uploadId"),
			UploadId: aws.String("multipartId"),
			MultipartUpload: &s3.CompletedMultipartUpload{
				Parts: []*s3.CompletedPart{
					{
						ETag:       aws.String("foo"),
						PartNumber: aws.Int64(1),
					},
					{
						ETag:       aws.String("bar"),
						PartNumber: aws.Int64(2),
					},
					{
						ETag:       aws.String("baz"),
						PartNumber: aws.Int64(3),
					},
				},
			},
		}).Return(nil, nil),
	)

	err := store.ConcatUploads("uploadId+multipartId", []string{
		"aaa+AAA",
		"bbb+BBB",
		"ccc+CCC",
	})
	assert.Nil(err)
}
