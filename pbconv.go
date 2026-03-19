package a2ahistory

import (
	"fmt"

	"github.com/a2aproject/a2a-go/v2/a2a"
	v1 "go.alis.build/a2a/lf/a2a/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func toProtoTask(task *a2a.Task) (*v1.Task, error) {
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

	result := &v1.Task{
		Id:        string(task.ID),
		ContextId: task.ContextID,
		Status:    status,
		Artifacts: artifacts,
		History:   history,
		Metadata:  metadata,
	}
	return result, nil
}

func toProtoTaskStatusUpdate(statusUpdate *a2a.TaskStatusUpdateEvent) (*v1.TaskStatusUpdateEvent, error) {
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

	result := &v1.TaskStatusUpdateEvent{
		TaskId:    string(statusUpdate.TaskInfo().TaskID),
		ContextId: statusUpdate.ContextID,
		Status:    status,
		Metadata:  metadata,
	}
	return result, nil
}

func toProtoTaskArtifacteUpdate(artifactUpdate *a2a.TaskArtifactUpdateEvent) (*v1.TaskArtifactUpdateEvent, error) {
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

	result := &v1.TaskArtifactUpdateEvent{
		TaskId:    string(artifactUpdate.TaskID),
		ContextId: artifactUpdate.ContextID,
		Artifact:  artifact,
		Append:    artifactUpdate.Append,
		LastChunk: artifactUpdate.LastChunk,
		Metadata:  metadata,
	}
	return result, nil
}

func toProtoMessages(msgs []*a2a.Message) ([]*v1.Message, error) {
	pMsgs := make([]*v1.Message, len(msgs))
	for i, msg := range msgs {
		pMsg, err := toProtoMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert message: %w", err)
		}
		pMsgs[i] = pMsg
	}
	return pMsgs, nil
}

func toProtoMessage(msg *a2a.Message) (*v1.Message, error) {
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
	if msg.ReferenceTasks != nil {
		taskIDs = make([]string, len(msg.ReferenceTasks))
		for i, tid := range msg.ReferenceTasks {
			taskIDs[i] = string(tid)
		}
	}

	return &v1.Message{
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

func toProtoParts(parts []*a2a.Part) ([]*v1.Part, error) {
	pParts := make([]*v1.Part, len(parts))
	for i, part := range parts {
		pPart, err := toProtoPart(part)
		if err != nil {
			return nil, fmt.Errorf("failed to convert part: %w", err)
		}
		pParts[i] = pPart
	}
	return pParts, nil
}

func toProtoPart(part *a2a.Part) (*v1.Part, error) {
	if part == nil {
		return nil, nil
	}

	var metadata *structpb.Struct
	if len(part.Metadata) > 0 {
		var err error
		metadata, err = structpb.NewStruct(part.Metadata)
		if err != nil {
			return nil, fmt.Errorf("failed to convert metadata to proto struct: %w", err)
		}
	}
	result := &v1.Part{
		Content:   nil,
		Metadata:  metadata,
		Filename:  part.Filename,
		MediaType: part.MediaType,
	}

	switch part.Content.(type) {
	case a2a.Text:
		result.Content = &v1.Part_Text{
			Text: part.Text(),
		}
	case a2a.Data:
		data, err := structpb.NewValue(part.Data())
		if err != nil {
			return nil, fmt.Errorf("failed to convert data to proto value: %w", err)
		}

		result.Content = &v1.Part_Data{
			Data: data,
		}
	case a2a.URL:
		result.Content = &v1.Part_Url{
			Url: string(part.URL()),
		}
	case a2a.Raw:
		result.Content = &v1.Part_Raw{
			Raw: part.Raw(),
		}
	default:
		return nil, fmt.Errorf("unsupported part type: %T", part.Content)
	}

	return result, nil
}

func toProtoArtifacts(artifacts []*a2a.Artifact) ([]*v1.Artifact, error) {
	result := make([]*v1.Artifact, len(artifacts))
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

func toProtoArtifact(artifact *a2a.Artifact) (*v1.Artifact, error) {
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

	return &v1.Artifact{
		ArtifactId:  string(artifact.ID),
		Name:        artifact.Name,
		Description: artifact.Description,
		Parts:       parts,
		Metadata:    metadata,
		Extensions:  artifact.Extensions,
	}, nil
}

func toProtoTaskStatus(status a2a.TaskStatus) (*v1.TaskStatus, error) {
	message, err := toProtoMessage(status.Message)
	if err != nil {
		return nil, fmt.Errorf("failed to convert message for task status: %w", err)
	}

	pStatus := &v1.TaskStatus{
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

func toProtoValue(data map[string]any) (*structpb.Value, error) {
	if data == nil {
		return nil, nil
	}
	s, err := structpb.NewValue(data)
	if err != nil {
		return nil, fmt.Errorf("failed to convert metadata to proto value: %w", err)
	}
	return s, nil
}

func toProtoRole(role a2a.MessageRole) v1.Role {
	switch role {
	case a2a.MessageRoleUser:
		return v1.Role_ROLE_USER
	case a2a.MessageRoleAgent:
		return v1.Role_ROLE_AGENT
	default:
		return v1.Role_ROLE_UNSPECIFIED
	}
}

func toProtoTaskState(state a2a.TaskState) v1.TaskState {
	switch state {
	case a2a.TaskStateAuthRequired:
		return v1.TaskState_TASK_STATE_AUTH_REQUIRED
	case a2a.TaskStateCanceled:
		return v1.TaskState_TASK_STATE_CANCELED
	case a2a.TaskStateCompleted:
		return v1.TaskState_TASK_STATE_COMPLETED
	case a2a.TaskStateFailed:
		return v1.TaskState_TASK_STATE_FAILED
	case a2a.TaskStateInputRequired:
		return v1.TaskState_TASK_STATE_INPUT_REQUIRED
	case a2a.TaskStateRejected:
		return v1.TaskState_TASK_STATE_REJECTED
	case a2a.TaskStateSubmitted:
		return v1.TaskState_TASK_STATE_SUBMITTED
	case a2a.TaskStateWorking:
		return v1.TaskState_TASK_STATE_WORKING
	default:
		return v1.TaskState_TASK_STATE_UNSPECIFIED
	}
}
