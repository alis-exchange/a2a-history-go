package a2asrv

import (
	"fmt"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func toProtoTask(task *a2a.Task) (*a2apb.Task, error) {
	if task == nil {
		return nil, nil
	}

	status, err := toProtoTaskStatus(task.Status)
	if err != nil {
		return nil, fmt.Errorf("failed to convert status: %w", err)
	}

	artifacts, err := toProtoArtifacts(task.Artifacts)
	if err != nil {
		return nil, fmt.Errorf("failed to convert artifacts: %w", err)
	}

	history, err := toProtoMessages(task.History)
	if err != nil {
		return nil, fmt.Errorf("failed to convert history: %w", err)
	}

	metadata, err := toProtoStruct(task.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to convert metadata to proto struct: %w", err)
	}

	result := &a2apb.Task{
		Id:        string(task.ID),
		ContextId: task.ContextID,
		Status:    status,
		Artifacts: artifacts,
		History:   history,
		Metadata:  metadata,
	}
	return result, nil
}

func toProtoTaskStatusUpdate(statusUpdate *a2a.TaskStatusUpdateEvent) (*a2apb.TaskStatusUpdateEvent, error) {
	if statusUpdate == nil {
		return nil, nil
	}

	status, err := toProtoTaskStatus(statusUpdate.Status)
	if err != nil {
		return nil, fmt.Errorf("failed to convert status: %w", err)
	}

	metadata, err := toProtoStruct(statusUpdate.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to convert metadata to proto struct: %w", err)
	}

	result := &a2apb.TaskStatusUpdateEvent{
		TaskId:    string(statusUpdate.TaskInfo().TaskID),
		ContextId: statusUpdate.ContextID,
		Status:    status,
		Metadata:  metadata,
	}
	return result, nil
}

func toProtoTaskArtifacteUpdate(artifactUpdate *a2a.TaskArtifactUpdateEvent) (*a2apb.TaskArtifactUpdateEvent, error) {
	if artifactUpdate == nil {
		return nil, nil
	}

	artifact, err := toProtoArtifact(artifactUpdate.Artifact)
	if err != nil {
		return nil, fmt.Errorf("failed to convert artifacts: %w", err)
	}

	metadata, err := toProtoStruct(artifactUpdate.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to convert metadata to proto struct: %w", err)
	}

	result := &a2apb.TaskArtifactUpdateEvent{
		TaskId:    string(artifactUpdate.TaskID),
		ContextId: artifactUpdate.ContextID,
		Artifact:  artifact,
		Append:    artifactUpdate.Append,
		LastChunk: artifactUpdate.LastChunk,
		Metadata:  metadata,
	}
	return result, nil
}

func toProtoMessages(msgs []*a2a.Message) ([]*a2apb.Message, error) {
	pMsgs := make([]*a2apb.Message, len(msgs))
	for i, msg := range msgs {
		pMsg, err := toProtoMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert message: %w", err)
		}
		pMsgs[i] = pMsg
	}
	return pMsgs, nil
}

func toProtoMessage(msg *a2a.Message) (*a2apb.Message, error) {
	if msg == nil {
		return nil, nil
	}

	parts, err := toProtoParts(msg.Parts)
	if err != nil {
		return nil, err
	}

	meta, err := toProtoStruct(msg.Metadata)
	if err != nil {
		return nil, err
	}

	var taskIDs []string
	if len(msg.ReferenceTasks) > 0 {
		taskIDs = make([]string, len(msg.ReferenceTasks))
		for i, tid := range msg.ReferenceTasks {
			taskIDs[i] = string(tid)
		}
	}

	return &a2apb.Message{
		MessageId:        msg.ID,
		ContextId:        msg.ContextID,
		TaskId:           string(msg.TaskID),
		Role:             toProtoRole(msg.Role),
		Parts:            parts,
		Metadata:         meta,
		Extensions:       msg.Extensions,
		ReferenceTaskIds: taskIDs,
	}, nil
}

func toProtoParts(parts a2a.ContentParts) ([]*a2apb.Part, error) {
	pParts := make([]*a2apb.Part, len(parts))
	for i, part := range parts {
		pPart, err := toProtoPart(part)
		if err != nil {
			return nil, fmt.Errorf("failed to convert part: %w", err)
		}
		pParts[i] = pPart
	}
	return pParts, nil
}

func toProtoPart(part *a2a.Part) (*a2apb.Part, error) {
	switch p := part.Content.(type) {
	case a2a.Text:
		return toProtoTextPart(part)
	case a2a.Data:
		return toProtoDataPart(part)
	case a2a.Raw:
		return toProtoFilePart(part)
	case a2a.URL:
		return toProtoURLPart(part)
	default:
		return nil, fmt.Errorf("unsupported part type: %T", p)
	}
}

func toProtoTextPart(part *a2a.Part) (*a2apb.Part, error) {
	metadata, err := toProtoStruct(part.Metadata)
	if err != nil {
		return nil, err
	}
	return &a2apb.Part{
		Content: &a2apb.Part_Text{
			Text: part.Text(),
		},
		Metadata:  metadata,
		MediaType: "text/plain",
	}, nil
}

func toProtoDataPart(part *a2a.Part) (*a2apb.Part, error) {
	data, err := toProtoValue(part.Data())
	if err != nil {
		return nil, err
	}
	metadata, err := toProtoStruct(part.Metadata)
	if err != nil {
		return nil, err
	}
	return &a2apb.Part{
		Content: &a2apb.Part_Data{
			Data: data,
		},
		Metadata: metadata,
	}, nil
}

func toProtoFilePart(part *a2a.Part) (*a2apb.Part, error) {
	metadata, err := toProtoStruct(part.Metadata)
	if err != nil {
		return nil, err
	}
	return &a2apb.Part{
		Content: &a2apb.Part_Raw{
			Raw: part.Raw(),
		},
		Metadata:  metadata,
		Filename:  part.Filename,
		MediaType: part.MediaType,
	}, nil
}

func toProtoURLPart(part *a2a.Part) (*a2apb.Part, error) {
	metadata, err := toProtoStruct(part.Metadata)
	if err != nil {
		return nil, err
	}

	return &a2apb.Part{
		Content: &a2apb.Part_Url{
			Url: string(part.URL()),
		},
		Metadata:  metadata,
		MediaType: part.MediaType,
		Filename:  part.Filename,
	}, nil
}

func toProtoArtifacts(artifacts []*a2a.Artifact) ([]*a2apb.Artifact, error) {
	result := make([]*a2apb.Artifact, len(artifacts))
	for i, artifact := range artifacts {
		pArtifact, err := toProtoArtifact(artifact)
		if err != nil {
			return nil, fmt.Errorf("failed to convert artifact: %w", err)
		}
		if pArtifact != nil {
			result[i] = pArtifact
		}
	}
	return result, nil
}

func toProtoArtifact(artifact *a2a.Artifact) (*a2apb.Artifact, error) {
	if artifact == nil {
		return nil, nil
	}

	metadata, err := toProtoStruct(artifact.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to convert metadata to proto struct: %w", err)
	}

	parts, err := toProtoParts(artifact.Parts)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to proto parts: %w", err)
	}

	return &a2apb.Artifact{
		ArtifactId:  string(artifact.ID),
		Name:        artifact.Name,
		Description: artifact.Description,
		Parts:       parts,
		Metadata:    metadata,
		Extensions:  artifact.Extensions,
	}, nil
}

func toProtoTaskStatus(status a2a.TaskStatus) (*a2apb.TaskStatus, error) {
	message, err := toProtoMessage(status.Message)
	if err != nil {
		return nil, fmt.Errorf("failed to convert message for task status: %w", err)
	}

	pStatus := &a2apb.TaskStatus{
		State:   toProtoTaskState(status.State),
		Message: message,
	}
	if status.Timestamp != nil {
		pStatus.Timestamp = timestamppb.New(*status.Timestamp)
	}

	return pStatus, nil
}

func toProtoStruct(data map[string]any) (*structpb.Struct, error) {
	if data == nil {
		return nil, nil
	}
	s, err := structpb.NewStruct(data)
	if err != nil {
		return nil, fmt.Errorf("failed to convert metadata to proto struct: %w", err)
	}
	return s, nil
}

func toProtoValue(data any) (*structpb.Value, error) {
	s, err := structpb.NewValue(data)
	if err != nil {
		return nil, fmt.Errorf("failed to convert metadata to proto value: %w", err)
	}
	return s, nil
}

func toProtoRole(role a2a.MessageRole) a2apb.Role {
	switch role {
	case a2a.MessageRoleUser:
		return a2apb.Role_ROLE_USER
	case a2a.MessageRoleAgent:
		return a2apb.Role_ROLE_AGENT
	default:
		return a2apb.Role_ROLE_UNSPECIFIED
	}
}

func toProtoTaskState(state a2a.TaskState) a2apb.TaskState {
	switch state {
	case a2a.TaskStateAuthRequired:
		return a2apb.TaskState_TASK_STATE_AUTH_REQUIRED
	case a2a.TaskStateCanceled:
		return a2apb.TaskState_TASK_STATE_CANCELED
	case a2a.TaskStateCompleted:
		return a2apb.TaskState_TASK_STATE_COMPLETED
	case a2a.TaskStateFailed:
		return a2apb.TaskState_TASK_STATE_FAILED
	case a2a.TaskStateInputRequired:
		return a2apb.TaskState_TASK_STATE_INPUT_REQUIRED
	case a2a.TaskStateRejected:
		return a2apb.TaskState_TASK_STATE_REJECTED
	case a2a.TaskStateSubmitted:
		return a2apb.TaskState_TASK_STATE_SUBMITTED
	case a2a.TaskStateWorking:
		return a2apb.TaskState_TASK_STATE_WORKING
	default:
		return a2apb.TaskState_TASK_STATE_UNSPECIFIED
	}
}
