terraform {
  required_version = ">= 1.0"
  
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  backend "s3" {
    bucket         = "mini-lambda-terraform-state"
    key            = "infrastructure/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "mini-lambda-terraform-locks"
    encrypt        = true
  }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "mini-lambda"
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}

# Variables
variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment name"
  type        = string
  default     = "production"
}

variable "app_name" {
  description = "Application name"
  type        = string
  default     = "mini-lambda"
}

# VPC
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"

  name = "${var.app_name}-vpc"
  cidr = "10.0.0.0/16"

  azs             = ["${var.aws_region}a", "${var.aws_region}b", "${var.aws_region}c"]
  private_subnets = ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"]
  public_subnets  = ["10.0.101.0/24", "10.0.102.0/24", "10.0.103.0/24"]

  enable_nat_gateway   = true
  single_nat_gateway   = var.environment != "production"
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "${var.app_name}-vpc"
  }
}

# Security Groups
resource "aws_security_group" "rds" {
  name        = "${var.app_name}-rds-sg"
  description = "Security group for RDS PostgreSQL"
  vpc_id      = module.vpc.vpc_id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.app.id]
    description     = "PostgreSQL from app"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.app_name}-rds-sg"
  }
}

resource "aws_security_group" "mq" {
  name        = "${var.app_name}-mq-sg"
  description = "Security group for Amazon MQ"
  vpc_id      = module.vpc.vpc_id

  ingress {
    from_port       = 5672
    to_port         = 5672
    protocol        = "tcp"
    security_groups = [aws_security_group.app.id]
    description     = "AMQP from app"
  }

  ingress {
    from_port       = 15672
    to_port         = 15672
    protocol        = "tcp"
    security_groups = [aws_security_group.app.id]
    description     = "Management console"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.app_name}-mq-sg"
  }
}

resource "aws_security_group" "app" {
  name        = "${var.app_name}-app-sg"
  description = "Security group for application"
  vpc_id      = module.vpc.vpc_id

  ingress {
    from_port   = 8080
    to_port     = 8080
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "HTTP"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.app_name}-app-sg"
  }
}

# RDS PostgreSQL
resource "aws_db_subnet_group" "main" {
  name       = "${var.app_name}-db-subnet"
  subnet_ids = module.vpc.private_subnets

  tags = {
    Name = "${var.app_name}-db-subnet"
  }
}

resource "aws_db_instance" "postgres" {
  identifier     = "${var.app_name}-postgres"
  engine         = "postgres"
  engine_version = "15.4"
  
  instance_class        = var.environment == "production" ? "db.t3.small" : "db.t3.micro"
  allocated_storage     = 20
  max_allocated_storage = 100
  storage_encrypted     = true

  db_name  = "mini_lambda"
  username = "postgres"
  password = random_password.db_password.result

  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.rds.id]
  
  backup_retention_period = var.environment == "production" ? 7 : 1
  skip_final_snapshot     = var.environment != "production"
  final_snapshot_identifier = var.environment == "production" ? "${var.app_name}-final-snapshot-${formatdate("YYYY-MM-DD-hhmm", timestamp())}" : null

  multi_az               = var.environment == "production"
  publicly_accessible    = false
  deletion_protection    = var.environment == "production"

  tags = {
    Name = "${var.app_name}-postgres"
  }
}

resource "random_password" "db_password" {
  length  = 32
  special = true
}

# Amazon MQ (RabbitMQ)
resource "aws_mq_broker" "rabbitmq" {
  broker_name        = "${var.app_name}-rabbitmq"
  engine_type        = "RabbitMQ"
  engine_version     = "3.12"
  host_instance_type = var.environment == "production" ? "mq.t3.micro" : "mq.t3.micro"
  
  deployment_mode = var.environment == "production" ? "CLUSTER_MULTI_AZ" : "SINGLE_INSTANCE"
  
  subnet_ids         = var.environment == "production" ? module.vpc.private_subnets : [module.vpc.private_subnets[0]]
  security_groups    = [aws_security_group.mq.id]
  publicly_accessible = false

  user {
    username = "admin"
    password = random_password.mq_password.result
  }

  logs {
    general = true
  }

  tags = {
    Name = "${var.app_name}-rabbitmq"
  }
}

resource "random_password" "mq_password" {
  length  = 32
  special = true
}

# S3 Bucket
resource "aws_s3_bucket" "lambda_functions" {
  bucket = "${var.app_name}-functions-${data.aws_caller_identity.current.account_id}"

  tags = {
    Name = "${var.app_name}-functions"
  }
}

resource "aws_s3_bucket_versioning" "lambda_functions" {
  bucket = aws_s3_bucket.lambda_functions.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_encryption" "lambda_functions" {
  bucket = aws_s3_bucket.lambda_functions.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "lambda_functions" {
  bucket = aws_s3_bucket.lambda_functions.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# ECS Cluster
resource "aws_ecs_cluster" "main" {
  name = "${var.app_name}-cluster"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }

  tags = {
    Name = "${var.app_name}-cluster"
  }
}

resource "aws_ecs_cluster_capacity_providers" "main" {
  cluster_name = aws_ecs_cluster.main.name

  capacity_providers = ["FARGATE", "FARGATE_SPOT"]

  default_capacity_provider_strategy {
    capacity_provider = var.environment == "production" ? "FARGATE" : "FARGATE_SPOT"
    weight            = 1
    base              = var.environment == "production" ? 2 : 0
  }
}

# IAM Role for ECS Task Execution
resource "aws_iam_role" "ecs_task_execution" {
  name = "${var.app_name}-ecs-task-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "ecs_task_execution" {
  role       = aws_iam_role.ecs_task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# IAM Role for ECS Task (Application)
resource "aws_iam_role" "ecs_task" {
  name = "${var.app_name}-ecs-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy" "ecs_task_s3" {
  name = "${var.app_name}-s3-access"
  role = aws_iam_role.ecs_task.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:ListBucket"
      ]
      Resource = [
        aws_s3_bucket.lambda_functions.arn,
        "${aws_s3_bucket.lambda_functions.arn}/*"
      ]
    }]
  })
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "app" {
  name              = "/ecs/${var.app_name}"
  retention_in_days = var.environment == "production" ? 30 : 7

  tags = {
    Name = "${var.app_name}-logs"
  }
}

# Application Load Balancer
resource "aws_lb" "app" {
  name               = "${var.app_name}-alb"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = module.vpc.public_subnets

  enable_deletion_protection = var.environment == "production"

  tags = {
    Name = "${var.app_name}-alb"
  }
}

resource "aws_security_group" "alb" {
  name        = "${var.app_name}-alb-sg"
  description = "Security group for ALB"
  vpc_id      = module.vpc.vpc_id

  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.app_name}-alb-sg"
  }
}

resource "aws_lb_target_group" "app" {
  name        = "${var.app_name}-tg"
  port        = 8080
  protocol    = "HTTP"
  vpc_id      = module.vpc.vpc_id
  target_type = "ip"

  health_check {
    enabled             = true
    path                = "/health"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    timeout             = 5
    interval            = 30
    matcher             = "200"
  }

  deregistration_delay = 30

  tags = {
    Name = "${var.app_name}-tg"
  }
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.app.arn
  port              = "80"
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}

# Data sources
data "aws_caller_identity" "current" {}

# Outputs
output "vpc_id" {
  value = module.vpc.vpc_id
}

output "rds_endpoint" {
  value     = aws_db_instance.postgres.endpoint
  sensitive = true
}

output "rds_password" {
  value     = random_password.db_password.result
  sensitive = true
}

output "mq_endpoints" {
  value     = aws_mq_broker.rabbitmq.instances
  sensitive = true
}

output "mq_password" {
  value     = random_password.mq_password.result
  sensitive = true
}

output "s3_bucket" {
  value = aws_s3_bucket.lambda_functions.id
}

output "ecs_cluster_name" {
  value = aws_ecs_cluster.main.name
}

output "alb_dns_name" {
  value = aws_lb.app.dns_name
}

output "task_execution_role_arn" {
  value = aws_iam_role.ecs_task_execution.arn
}

output "task_role_arn" {
  value = aws_iam_role.ecs_task.arn
}